package watcher

import (
	"context"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

type Lister interface {
	List(context.Context) ([]client.Object, error)
}

type Admitter interface {
	// todo: add reason
	ShouldAdmit(context.Context) (bool, error)
}

type Watcher struct {
	eventsChannel chan<- event.GenericEvent
	condition     bool
	lister        Lister
	admitter      Admitter
	period        time.Duration
}

func NewWatcher(
	lister Lister,
	admitter Admitter,
	period time.Duration,
) (*Watcher, <-chan event.GenericEvent) {
	ch := make(chan event.GenericEvent)

	return &Watcher{
		eventsChannel: ch,
		lister:        lister,
		admitter:      admitter,
		period:        period,
	}, ch
}

func (w *Watcher) Start(ctx context.Context) error {
	doneCh := ctx.Done()
	ticker := time.NewTicker(w.period)

	go func() {
		for {
			select {
			case <-doneCh:
				ctrl.LoggerFrom(ctx).Info("Received done signal")
				return
			case <-ticker.C:
				if err := w.Run(ctx); err != nil {
					// todo: add proper monitoring
					panic(err)
				}
			}
		}
	}()

	return nil
}

func (w *Watcher) Run(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Running Query")

	shouldAdmit, err := w.admitter.ShouldAdmit(ctx)
	if err != nil {
		// should we change the condition? how to avoid blocking the condition on false?
		return err
	}

	// nothing to do
	if w.condition == shouldAdmit {
		return nil
	}

	w.condition = shouldAdmit
	l.Info("Condition changed, sending notification")
	return w.notify(ctx)
}

func (w *Watcher) ShouldAdmit(ctx context.Context) (bool, error) {
	return w.condition, nil
}

func (w *Watcher) notify(ctx context.Context) error {
	objects, err := w.lister.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range objects {
		if o == nil {
			ctrl.LoggerFrom(ctx).Info("Got nil object")
		}
		w.eventsChannel <- event.GenericEvent{Object: o}
	}
	return nil
}

type ConfigAdmitter struct {
	client          client.Client
	configMapNsName types.NamespacedName
	logger          logr.Logger
}

var _ Admitter = &ConfigAdmitter{}

func NewConfigAdmitter(
	client client.Client,
	configMapNsName types.NamespacedName,
	logger logr.Logger,
) *ConfigAdmitter {
	return &ConfigAdmitter{
		client:          client,
		configMapNsName: configMapNsName,
		logger:          logger,
	}
}

// ShouldAdmit implements Admitter.
func (c *ConfigAdmitter) ShouldAdmit(ctx context.Context) (bool, error) {
	cm := &corev1.ConfigMap{}
	err := c.client.Get(ctx, c.configMapNsName, cm)
	if err != nil {
		// need to add error handling
		c.logger.Error(err, "Failed to get configmap")
		return false, err
	}

	shouldAdmit, err := strconv.ParseBool(cm.Data["shouldAdmit"])
	if err != nil {
		return false, nil
	}
	c.logger.Info("Should admit equals", "shouldAdmit", shouldAdmit)
	return shouldAdmit, nil
}
