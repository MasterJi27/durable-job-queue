package memory

import (
	"context"
	"durablequeue/queue"
	"errors"
	"sort"
	"strings"
)

func (s *Store) Result(ctx context.Context, id string) (queue.Result, error) {
	if err := ctx.Err(); err != nil {
		return queue.Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[id]
	if !ok {
		return r, queue.ErrNotFound
	}
	r.Value = append(r.Value[:0:0], r.Value...)
	return r, nil
}
func (s *Store) Inspect(ctx context.Context, q string, status queue.Status, search string, offset, limit int) (queue.Inspection, error) {
	out := queue.Inspection{Queue: q, Offset: offset, Limit: limit, Jobs: []queue.Job{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return out, errors.New("offset must be nonnegative and limit 1..100")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out.ServerTime = s.now()
	var matches []queue.Job
	search = strings.ToLower(search)
	for _, j := range s.jobs {
		if j.Queue != q {
			continue
		}
		out.Stats.Total++
		switch j.Status {
		case queue.Pending:
			out.Stats.Pending++
		case queue.Running:
			out.Stats.Running++
		case queue.Succeeded:
			out.Stats.Succeeded++
		case queue.Dead:
			out.Stats.Dead++
		}
		if (status == "" || status == j.Status) && strings.Contains(strings.ToLower(j.ID+" "+j.Kind+" "+j.Key), search) {
			matches = append(matches, j)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].ID > matches[j].ID
		}
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	out.Filtered = len(matches)
	if offset < len(matches) {
		end := min(offset+limit, len(matches))
		for _, j := range matches[offset:end] {
			out.Jobs = append(out.Jobs, j.Clone())
		}
	}
	return out, nil
}
