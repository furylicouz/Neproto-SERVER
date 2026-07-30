package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const webAPIMaximumJobs = 64

var errWebAPIJobCapacity = errors.New("web control job capacity exhausted")

type webAPIJobState string

const (
	webAPIJobQueued    webAPIJobState = "queued"
	webAPIJobRunning   webAPIJobState = "running"
	webAPIJobSucceeded webAPIJobState = "succeeded"
	webAPIJobFailed    webAPIJobState = "failed"
)

type webAPIJob struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	State      webAPIJobState `json:"state"`
	Progress   int            `json:"progress"`
	Stage      string         `json:"stage"`
	Error      string         `json:"error,omitempty"`
	Result     any            `json:"result,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

type webAPIJobStore struct {
	mutex    sync.Mutex
	mutation *sync.Mutex
	jobs     map[string]webAPIJob
	order    []string
}

func newWebAPIJobStore(mutation *sync.Mutex) *webAPIJobStore {
	return &webAPIJobStore{mutation: mutation, jobs: make(map[string]webAPIJob)}
}

func (store *webAPIJobStore) start(kind string, work func(func(int, string)) (any, error)) (webAPIJob, error) {
	rawID := make([]byte, 16)
	if _, err := rand.Read(rawID); err != nil {
		return webAPIJob{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(rawID)
	for index := range rawID {
		rawID[index] = 0
	}
	job := webAPIJob{ID: id, Kind: kind, State: webAPIJobQueued, Progress: 0, Stage: "Queued", CreatedAt: time.Now().UTC()}
	store.mutex.Lock()
	if !store.pruneLocked() {
		store.mutex.Unlock()
		return webAPIJob{}, errWebAPIJobCapacity
	}
	store.jobs[id] = job
	store.order = append(store.order, id)
	store.mutex.Unlock()
	go store.run(id, work)
	return job, nil
}

func (store *webAPIJobStore) run(id string, work func(func(int, string)) (any, error)) {
	store.mutation.Lock()
	defer store.mutation.Unlock()
	defer func() {
		if recover() != nil {
			store.finish(id, nil, errors.New("background operation stopped unexpectedly"))
		}
	}()
	started := time.Now().UTC()
	store.update(id, func(job *webAPIJob) {
		job.State = webAPIJobRunning
		job.Progress = 1
		job.Stage = "Starting"
		job.StartedAt = &started
	})
	report := func(progress int, stage string) {
		store.update(id, func(job *webAPIJob) {
			if progress < 1 {
				progress = 1
			}
			if progress > 99 {
				progress = 99
			}
			job.Progress = progress
			job.Stage = boundedDisplay(stage, 240)
		})
	}
	result, err := work(report)
	store.finish(id, result, err)
}

func (store *webAPIJobStore) finish(id string, result any, err error) {
	finished := time.Now().UTC()
	store.update(id, func(job *webAPIJob) {
		job.FinishedAt = &finished
		if err != nil {
			job.State = webAPIJobFailed
			job.Error = boundedDisplay(err.Error(), 480)
			job.Stage = "Failed"
			return
		}
		job.State = webAPIJobSucceeded
		job.Progress = 100
		job.Stage = "Completed"
		job.Result = result
	})
}

func (store *webAPIJobStore) get(id string) (webAPIJob, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	job, exists := store.jobs[id]
	return job, exists
}

func (store *webAPIJobStore) update(id string, mutate func(*webAPIJob)) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	job, exists := store.jobs[id]
	if !exists {
		return
	}
	mutate(&job)
	store.jobs[id] = job
}

func (store *webAPIJobStore) pruneLocked() bool {
	for len(store.order) >= webAPIMaximumJobs {
		removable := -1
		for index, id := range store.order {
			job := store.jobs[id]
			if job.State != webAPIJobQueued && job.State != webAPIJobRunning {
				removable = index
				break
			}
		}
		if removable < 0 {
			return false
		}
		delete(store.jobs, store.order[removable])
		store.order = append(store.order[:removable], store.order[removable+1:]...)
	}
	return true
}

func (api *webAPI) writeJob(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/v1/jobs/")
	if strings.Contains(id, "/") || len(id) != 22 {
		api.writeError(writer, http.StatusNotFound, "job_not_found", "job not found")
		return
	}
	job, exists := api.jobs.get(id)
	if !exists {
		api.writeError(writer, http.StatusNotFound, "job_not_found", "job not found")
		return
	}
	api.writeJSON(writer, http.StatusOK, job)
}

func (api *webAPI) startJob(writer http.ResponseWriter, kind string, work func(func(int, string)) (any, error)) {
	job, err := api.jobs.start(kind, work)
	if err != nil {
		if errors.Is(err, errWebAPIJobCapacity) {
			api.writeError(writer, http.StatusTooManyRequests, "job_capacity_exhausted", "too many control operations are already queued")
			return
		}
		api.writeInternalError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusAccepted, job)
}
