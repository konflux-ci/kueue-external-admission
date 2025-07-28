package manager

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/monitoring"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

type AdmitterManager struct {
	logger    logr.Logger
	admitters map[string]*AdmitterEntry
}

type AdmitterEntry struct {
	Admitter           admission.Admitter
	AdmissionCheckName string
	Cancel             context.CancelFunc
}

type AdmitterCMD interface{}

type AdmitterCMDAdd struct {
	AdmissionCheckName string
	Admitter           admission.Admitter
}

type AdmitterCMDRemove struct {
	AdmissionCheckName string
}

type AdmitterCMDList struct {
	Result chan map[string]bool
}

func NewAdmitterManager(logger logr.Logger) *AdmitterManager {
	return &AdmitterManager{
		logger:    logger,
		admitters: make(map[string]*AdmitterEntry),
	}
}

func (m *AdmitterManager) Run(
	ctx context.Context,
	admitterCommands chan AdmitterCMD,
	incomingResults chan result.AsyncAdmissionResult,
) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping ManageAdmitters, context done")
			for admissionCheckName := range m.admitters {
				m.removeAdmitter(admissionCheckName)
			}
			return
		case cmd := <-admitterCommands:
			// TODO: generate and id for the admitter
			switch c := cmd.(type) {
			case AdmitterCMDAdd:
				m.setAdmitter(ctx, c.AdmissionCheckName, c.Admitter, admitterCommands, incomingResults)
			case AdmitterCMDRemove:
				m.removeAdmitter(c.AdmissionCheckName)
			case AdmitterCMDList:
				admitters := make(map[string]bool)
				for admissionCheckName := range m.admitters {
					admitters[admissionCheckName] = true
				}
				c.Result <- admitters
			default:
				m.logger.Error(fmt.Errorf("unknown command type"), "Unknown command type", "command", cmd)
			}
		}
	}
}

func (m *AdmitterManager) removeAdmitter(admissionCheckName string) {
	entry, ok := m.admitters[admissionCheckName]
	if !ok {
		m.logger.Error(fmt.Errorf("admitter not found"), "Admitter not found", "admissionCheck", admissionCheckName)
		return
	}
	entry.Cancel()
	delete(m.admitters, admissionCheckName)
	admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)
	admissionMetrics.DeleteAdmissionCheckStatus()
	m.logger.Info("Removed admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
}

func (m *AdmitterManager) setAdmitter(
	ctx context.Context,
	admissionCheckName string,
	admitter admission.Admitter,
	admitterCommands chan<- AdmitterCMD,
	incomingResults chan result.AsyncAdmissionResult,
) {
	entry, ok := m.admitters[admissionCheckName]
	if ok && reflect.DeepEqual(entry.Admitter, admitter) {
		m.logger.Info("Admitter already set, skipping", "admissionCheck", admissionCheckName)
		return
	} else if ok {
		m.logger.Info("Replacing existing admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
		m.removeAdmitter(admissionCheckName)
		m.logger.Info("Removed old admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}

	ctx, cancel := context.WithCancel(ctx)
	m.admitters[admissionCheckName] = &AdmitterEntry{
		AdmissionCheckName: admissionCheckName,
		Admitter:           admitter,
		Cancel:             cancel,
	}

	if err := admitter.Sync(ctx, incomingResults); err != nil {
		retryIn := 15 * time.Second
		m.logger.Error(err, "Failed to sync admitter", "admissionCheck", admissionCheckName, "retryIn", retryIn)
		go func() {
			time.Sleep(retryIn)
			admitterCommands <- AdmitterCMDAdd{
				AdmissionCheckName: admissionCheckName,
				Admitter:           admitter,
			}
		}()
	}
	// Set initial status to true just to make sure that the metric is set
	monitoring.NewAdmissionMetrics(admissionCheckName).RecordAdmissionCheckStatus(false)
	m.logger.Info("Added admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
}
