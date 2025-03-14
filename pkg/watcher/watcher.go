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
	List() ([]client.Object, error)
}

type Admitter interface {
	// todo: add reason
	ShouldAdmit() (bool, error)
}

type Watcher struct {
	eventsChannel chan<- event.GenericEvent
	condition     bool
	lister        Lister
	admitter      Admitter
	period        time.Duration
	logger        logr.Logger
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
	w.logger = ctrl.LoggerFrom(ctx)

	go func() {
		for {
			select {
			case <-doneCh:
				w.logger.Info("Received done signal")
				return
			case <-ticker.C:
				err := w.Run()
				if err != nil {
					// todo: add proper monitoring
					panic(err)
				}
			}
		}
	}()

	return nil
}

func (w *Watcher) Run() error {
	// todo: should we propagate a context to Query and notifiy?
	w.logger.Info("Running Query")
	shouldAdmit, err := w.admitter.ShouldAdmit()
	if err != nil {
		// should we change the condition? how to avoid blocking the condition on false?
		return err
	}
	if w.condition != shouldAdmit {
		w.condition = shouldAdmit
		w.logger.Info("Condition changed, sending notification")
		err = w.notify()
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *Watcher) ShouldAdmit() (bool, error) {
	return w.condition, nil
}

func (w *Watcher) notify() error {
	objects, err := w.lister.List()
	if err != nil {
		return err
	}
	for _, o := range objects {
		if o == nil {
			w.logger.Info("Got nil object")
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
func (c *ConfigAdmitter) ShouldAdmit() (bool, error) {
	cm := &corev1.ConfigMap{}
	err := c.client.Get(context.TODO(), c.configMapNsName, cm)
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
