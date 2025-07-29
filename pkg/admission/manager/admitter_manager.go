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
	logger           logr.Logger
	admitters        map[string]*AdmitterEntry
	admitterCommands chan admitterCmdFunc
	incomingResults  chan<- result.AsyncAdmissionResult
}

type AdmitterEntry struct {
	Admitter           admission.Admitter
	AdmissionCheckName string
	Cancel             context.CancelFunc
}

type admitterCmdFunc func(admitterManager *AdmitterManager, ctx context.Context)

func NewAdmitterManager(
	logger logr.Logger,
	admitterCommands chan admitterCmdFunc,
	incomingResults chan<- result.AsyncAdmissionResult,
) *AdmitterManager {
	return &AdmitterManager{
		logger:           logger,
		admitters:        make(map[string]*AdmitterEntry),
		admitterCommands: admitterCommands,
		incomingResults:  incomingResults,
	}
}

func (m *AdmitterManager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping ManageAdmitters, context done")
			for admissionCheckName := range m.admitters {
				RemoveAdmitter(admissionCheckName)(m, ctx)
			}
			return
		case cmd := <-m.admitterCommands:
			cmd(m, ctx)
		}
	}
}

func SetAdmitter(admissionCheckName string, admitter admission.Admitter) admitterCmdFunc {
	return func(m *AdmitterManager, ctx context.Context) {
		entry, ok := m.admitters[admissionCheckName]
		if ok && reflect.DeepEqual(entry.Admitter, admitter) {
			m.logger.Info("Admitter already set, skipping", "admissionCheck", admissionCheckName)
			return
		} else if ok {
			m.logger.Info("Replacing existing admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
			RemoveAdmitter(admissionCheckName)(m, ctx)
			m.logger.Info("Removed old admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
		}

		ctx, cancel := context.WithCancel(ctx)
		m.admitters[admissionCheckName] = &AdmitterEntry{
			AdmissionCheckName: admissionCheckName,
			Admitter:           admitter,
			Cancel:             cancel,
		}

		if err := admitter.Sync(ctx, m.incomingResults); err != nil {
			retryIn := 15 * time.Second
			m.logger.Error(err, "Failed to sync admitter", "admissionCheck", admissionCheckName, "retryIn", retryIn)
			go func() {
				time.Sleep(retryIn)
				m.admitterCommands <- SetAdmitter(admissionCheckName, admitter)
			}()
		}
		// Set initial status to true just to make sure that the metric is set
		monitoring.NewAdmissionMetrics(admissionCheckName).RecordAdmissionCheckStatus(false)
		m.logger.Info("Added admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}
}

func RemoveAdmitter(admissionCheckName string) admitterCmdFunc {
	return func(m *AdmitterManager, ctx context.Context) {
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
}

func ListAdmitters() (admitterCmdFunc, <-chan map[string]bool) {
	resultChan := make(chan map[string]bool, 1)
	return func(m *AdmitterManager, ctx context.Context) {
		defer close(resultChan)
		admitters := make(map[string]bool)
		for admissionCheckName := range m.admitters {
			admitters[admissionCheckName] = true
		}
		resultChan <- admitters
	}, resultChan
}
