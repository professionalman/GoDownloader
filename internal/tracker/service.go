package tracker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"

	"github.com/google/uuid"
)

const (
	maxResponseBytes = 2 << 20
	maxTrackerLines  = 10000
)

var ErrNotFound = errors.New("tracker source not found")

type Service struct {
	repo   Repository
	client *http.Client
	bus    job.IEventBus
	sem    chan struct{}
	mu     sync.Mutex
	flight map[string]*sync.Mutex
}

func NewService(repo Repository, bus job.IEventBus) *Service {
	service := &Service{repo: repo, bus: bus, sem: make(chan struct{}, 4), flight: make(map[string]*sync.Mutex)}
	service.client = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("too many redirects")
			}
			if len(via) > 0 && !strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) {
				return fmt.Errorf("scheme-changing redirects are not allowed")
			}
			if req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
				return fmt.Errorf("unsafe redirect target")
			}
			return nil
		},
	}
	return service
}

func (s *Service) List(ctx context.Context) ([]networkpolicy.TrackerSource, error) {
	return s.repo.List(ctx)
}

func (s *Service) Entries(ctx context.Context, id string) ([]string, error) {
	return s.repo.Entries(ctx, id)
}

func (s *Service) EnabledEntries(ctx context.Context) ([]string, error) {
	return s.repo.EnabledEntries(ctx)
}

func (s *Service) Create(ctx context.Context, input networkpolicy.TrackerSourceInput) (*networkpolicy.TrackerSource, error) {
	if err := networkpolicy.ValidateTrackerSource(input); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	source := &networkpolicy.TrackerSource{
		ID: "tracker_" + uuid.NewString()[:8], Name: strings.TrimSpace(input.Name),
		URL: strings.TrimSpace(input.URL), Enabled: input.Enabled,
		RefreshIntervalSeconds: input.RefreshIntervalSeconds,
		CreatedAt:              now, UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, source); err != nil {
		return nil, err
	}
	return source, nil
}

func (s *Service) Update(ctx context.Context, id string, input networkpolicy.TrackerSourceInput) (*networkpolicy.TrackerSource, error) {
	if err := networkpolicy.ValidateTrackerSource(input); err != nil {
		return nil, err
	}
	source, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, ErrNotFound
	}
	source.Name = strings.TrimSpace(input.Name)
	source.URL = strings.TrimSpace(input.URL)
	source.Enabled = input.Enabled
	source.RefreshIntervalSeconds = input.RefreshIntervalSeconds
	source.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, source); err != nil {
		return nil, err
	}
	s.publish(job.EventTrackerSourceUpdated, map[string]any{"source": source})
	return source, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	source, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if source == nil {
		return ErrNotFound
	}
	return s.repo.Delete(ctx, id)
}

func (s *Service) sourceLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.flight[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.flight[id] = lock
	}
	return lock
}

func (s *Service) Refresh(ctx context.Context, id string) (*networkpolicy.TrackerSource, error) {
	lock := s.sourceLock(id)
	lock.Lock()
	defer lock.Unlock()
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	source, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, ErrNotFound
	}
	checkedAt := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	if source.ETag != "" {
		req.Header.Set("If-None-Match", source.ETag)
	}
	if source.LastModified != "" {
		req.Header.Set("If-Modified-Since", source.LastModified)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return source, s.fail(ctx, source, err, checkedAt)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		source.LastCheckedAt = &checkedAt
		source.LastError = ""
		source.UpdatedAt = checkedAt
		if err := s.repo.Update(ctx, source); err != nil {
			return source, err
		}
		return source, nil
	}
	if resp.StatusCode != http.StatusOK {
		return source, s.fail(ctx, source, fmt.Errorf("source returned HTTP %d", resp.StatusCode), checkedAt)
	}
	if resp.ContentLength > maxResponseBytes {
		return source, s.fail(ctx, source, fmt.Errorf("tracker source exceeds 2 MiB"), checkedAt)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return source, s.fail(ctx, source, fmt.Errorf("failed to read tracker source"), checkedAt)
	}
	if len(body) > maxResponseBytes {
		return source, s.fail(ctx, source, fmt.Errorf("tracker source exceeds 2 MiB"), checkedAt)
	}
	entries, err := parseEntries(strings.NewReader(string(body)))
	if err != nil {
		return source, s.fail(ctx, source, err, checkedAt)
	}
	source.ETag = resp.Header.Get("ETag")
	source.LastModified = resp.Header.Get("Last-Modified")
	source.LastCheckedAt = &checkedAt
	source.LastSuccessAt = &checkedAt
	source.LastError = ""
	source.TrackerCount = len(entries)
	source.UpdatedAt = checkedAt
	if err := s.repo.ReplaceEntries(ctx, source, entries); err != nil {
		return source, err
	}
	s.publish(job.EventTrackerSourceUpdated, map[string]any{"source": source})
	return source, nil
}

func parseEntries(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxResponseBytes+1)
	values := make([]string, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		values = append(values, line)
		if len(values) > maxTrackerLines {
			return nil, fmt.Errorf("tracker source exceeds 10000 lines")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("tracker source exceeds 2 MiB or contains an oversized line")
	}
	return networkpolicy.ValidateTrackerURLs(values, maxTrackerLines)
}

func (s *Service) fail(ctx context.Context, source *networkpolicy.TrackerSource, cause error, checkedAt time.Time) error {
	message := sanitizeError(cause.Error())
	_ = s.repo.RecordFailure(ctx, source.ID, message, checkedAt)
	source.LastCheckedAt = &checkedAt
	source.LastError = message
	s.publish(job.EventTrackerSourceFailed, map[string]any{"sourceId": source.ID, "message": message})
	return fmt.Errorf("%s", message)
}

func sanitizeError(message string) string {
	if parsed, err := url.Parse(message); err == nil && parsed.User != nil {
		parsed.User = nil
		message = parsed.String()
	}
	if len(message) > 300 {
		message = message[:300]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, message)
}

func (s *Service) RefreshAll(ctx context.Context) []error {
	sources, err := s.repo.List(ctx)
	if err != nil {
		return []error{err}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errorsOut []error
	for i := range sources {
		source := sources[i]
		if !source.Enabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Refresh(ctx, source.ID); err != nil {
				mu.Lock()
				errorsOut = append(errorsOut, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errorsOut
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sources, err := s.repo.List(ctx)
			if err != nil {
				continue
			}
			now := time.Now()
			for _, source := range sources {
				if !source.Enabled {
					continue
				}
				if source.LastCheckedAt == nil || now.Sub(*source.LastCheckedAt) >= time.Duration(source.RefreshIntervalSeconds)*time.Second {
					go s.Refresh(ctx, source.ID)
				}
			}
		}
	}
}

func (s *Service) publish(eventType string, data any) {
	if s.bus != nil {
		s.bus.Publish(job.Event{Type: eventType, Data: data})
	}
}
