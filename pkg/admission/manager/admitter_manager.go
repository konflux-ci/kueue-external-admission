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

func NewAdmitterManager(logger logr.Logger) *AdmitterManager {
	return &AdmitterManager{
		logger:    logger,
		admitters: make(map[string]*AdmitterEntry),
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
	admitterCommands chan<- AdmitterChangeRequest,
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
			admitterCommands <- AdmitterChangeRequest{
				AdmissionCheckName:        admissionCheckName,
				AdmitterChangeRequestType: AdmitterChangeRequestAdd,
				Admitter:                  admitter,
			}
		}()
	}
	admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)
	// Set initial status to true just to make sure that the metric is set
	admissionMetrics.RecordAdmissionCheckStatus(true)
	m.logger.Info("Added admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
}

func (m *AdmitterManager) Run(
	ctx context.Context,
	admitterCommands chan AdmitterChangeRequest,
	incomingResults chan result.AsyncAdmissionResult,
	removalNotifications chan<- string,
) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping ManageAdmitters, context done")
			for admissionCheckName := range m.admitters {
				m.removeAdmitter(admissionCheckName)
			}
			return
		case changeRequest := <-admitterCommands:
			// TODO: generate and id for the admitter
			switch changeRequest.AdmitterChangeRequestType {
			case AdmitterChangeRequestAdd:
				m.setAdmitter(ctx, changeRequest.AdmissionCheckName, changeRequest.Admitter, admitterCommands, incomingResults)
			case AdmitterChangeRequestRemove:
				m.removeAdmitter(changeRequest.AdmissionCheckName)
				// TODO: there might be a race condition here, if the admitter is removed and the result is published
				// need to consider a periodic cleanup of the results registry
				removalNotifications <- changeRequest.AdmissionCheckName
			}
		}
	}
}
