package job

import (
	"fmt"
	"testing"
	"time"
)

func TestPriorityScore(t *testing.T) {
	if got := PriorityScore(JobPriorityHigh); got != 200 {
		t.Fatalf("expected High=200, got %d", got)
	}
	if got := PriorityScore(JobPriorityNormal); got != 100 {
		t.Fatalf("expected Normal=100, got %d", got)
	}
	if got := PriorityScore(JobPriorityLow); got != 0 {
		t.Fatalf("expected Low=0, got %d", got)
	}
	if got := PriorityScore(JobPriority("unknown")); got != 100 {
		t.Fatalf("expected unknown=100, got %d", got)
	}
}

func TestAgingContribution_MonotonicAndBounded(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	prevBonus := int64(-1)
	// Check monotonicity across 100 minutes
	for m := 0; m <= 100; m++ {
		now := t0.Add(time.Duration(m) * time.Minute)
		bonus := AgingContribution(t0, now, cfg)
		if bonus < prevBonus {
			t.Fatalf("aging bonus decreased at minute %d: prev=%d, current=%d", m, prevBonus, bonus)
		}
		if bonus > cfg.MaxAgingBonus {
			t.Fatalf("aging bonus exceeded maximum bound %d: got %d at minute %d", cfg.MaxAgingBonus, bonus, m)
		}
		prevBonus = bonus
	}

	// Explicit milestone checks
	if b := AgingContribution(t0, t0, cfg); b != 0 {
		t.Fatalf("expected 0 bonus at t0, got %d", b)
	}
	if b := AgingContribution(t0, t0.Add(1*time.Minute), cfg); b != 5 {
		t.Fatalf("expected 5 bonus at 1min, got %d", b)
	}
	if b := AgingContribution(t0, t0.Add(20*time.Minute), cfg); b != 100 {
		t.Fatalf("expected 100 bonus at 20min, got %d", b)
	}
	if b := AgingContribution(t0, t0.Add(50*time.Minute), cfg); b != 250 {
		t.Fatalf("expected 250 bonus at 50min, got %d", b)
	}
	if b := AgingContribution(t0, t0.Add(120*time.Minute), cfg); b != 250 {
		t.Fatalf("expected 250 (max cap) bonus at 120min, got %d", b)
	}
}

func TestCompareQueuedJobs_BasePriorityWinsInitially(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	high := &QueuedJob{
		JobID:      "job-high",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-high", Priority: JobPriorityHigh},
	}
	norm := &QueuedJob{
		JobID:      "job-norm",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-norm", Priority: JobPriorityNormal},
	}
	low := &QueuedJob{
		JobID:      "job-low",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-low", Priority: JobPriorityLow},
	}

	if cmp := CompareQueuedJobs(high, norm, t0, cfg); cmp >= 0 {
		t.Fatalf("expected High to beat Normal initially, got %d", cmp)
	}
	if cmp := CompareQueuedJobs(norm, low, t0, cfg); cmp >= 0 {
		t.Fatalf("expected Normal to beat Low initially, got %d", cmp)
	}
	if cmp := CompareQueuedJobs(high, low, t0, cfg); cmp >= 0 {
		t.Fatalf("expected High to beat Low initially, got %d", cmp)
	}
}

func TestCompareQueuedJobs_WithinLaneManualOrderPreserved(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Two jobs in the same Normal lane with identical effective priority
	jPos1 := &QueuedJob{
		JobID:      "job-pos1",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-pos1", Priority: JobPriorityNormal},
	}
	jPos2 := &QueuedJob{
		JobID:      "job-pos2",
		Position:   2,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-pos2", Priority: JobPriorityNormal},
	}

	evalTime := t0.Add(15 * time.Minute)
	// jPos1 must win because Position 1 < Position 2 breaks the tie between equal effective ranks
	if cmp := CompareQueuedJobs(jPos1, jPos2, evalTime, cfg); cmp >= 0 {
		t.Fatalf("expected manual queue position 1 to win over position 2 when effective rank is equal, got %d", cmp)
	}
	if cmpRev := CompareQueuedJobs(jPos2, jPos1, evalTime, cfg); cmpRev <= 0 {
		t.Fatalf("expected reverse comparison to be positive, got %d", cmpRev)
	}
}

func TestCompareQueuedJobs_ConcreteABCCycleEliminated(t *testing.T) {
	cfg := DefaultAgingConfig()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// A: priority = low (0), position = 2, sufficiently old that effective priority = 250
	// 50 minutes at 5 pts/min gives 250 bonus points
	jobA := &QueuedJob{
		JobID:      "job-A",
		Position:   2,
		EnqueuedAt: now.Add(-50 * time.Minute),
		Job:        Job{ID: "job-A", Priority: JobPriorityLow},
	}

	// B: priority = normal (100), position = 1, new, effective priority = 100
	jobB := &QueuedJob{
		JobID:      "job-B",
		Position:   1,
		EnqueuedAt: now,
		Job:        Job{ID: "job-B", Priority: JobPriorityNormal},
	}

	// C: priority = low (0), position = 1, new, effective priority = 0
	jobC := &QueuedJob{
		JobID:      "job-C",
		Position:   1,
		EnqueuedAt: now,
		Job:        Job{ID: "job-C", Priority: JobPriorityLow},
	}

	// Verify effective priorities
	if effA := EffectivePriority(jobA, now, cfg); effA != 250 {
		t.Fatalf("expected eff(A)=250, got %d", effA)
	}
	if effB := EffectivePriority(jobB, now, cfg); effB != 100 {
		t.Fatalf("expected eff(B)=100, got %d", effB)
	}
	if effC := EffectivePriority(jobC, now, cfg); effC != 0 {
		t.Fatalf("expected eff(C)=0, got %d", effC)
	}

	// Pairwise checks:
	// A beats B (250 > 100) -> cmp(A, B) < 0
	cmpAB := CompareQueuedJobs(jobA, jobB, now, cfg)
	if cmpAB >= 0 {
		t.Fatalf("expected A < B, got cmp=%d", cmpAB)
	}

	// B beats C (100 > 0) -> cmp(B, C) < 0
	cmpBC := CompareQueuedJobs(jobB, jobC, now, cfg)
	if cmpBC >= 0 {
		t.Fatalf("expected B < C, got cmp=%d", cmpBC)
	}

	// A must beat C (250 > 0) -> cmp(A, C) < 0 (NO CYCLE!)
	cmpAC := CompareQueuedJobs(jobA, jobC, now, cfg)
	if cmpAC >= 0 {
		t.Fatalf("expected A < C (transitivity), got cmp=%d; comparison cycle detected", cmpAC)
	}

	// Asymmetry checks
	if cmpBA := CompareQueuedJobs(jobB, jobA, now, cfg); cmpBA <= 0 {
		t.Fatalf("expected B > A, got cmp=%d", cmpBA)
	}
	if cmpCB := CompareQueuedJobs(jobC, jobB, now, cfg); cmpCB <= 0 {
		t.Fatalf("expected C > B, got cmp=%d", cmpCB)
	}
	if cmpCA := CompareQueuedJobs(jobC, jobA, now, cfg); cmpCA <= 0 {
		t.Fatalf("expected C > A, got cmp=%d", cmpCA)
	}

	// Ranking must yield [jobA, jobB, jobC]
	list := []QueuedJob{*jobC, *jobA, *jobB}
	RankQueuedJobs(list, now, cfg)
	expectedOrder := []string{"job-A", "job-B", "job-C"}
	for i, exp := range expectedOrder {
		if list[i].JobID != exp {
			t.Fatalf("index %d: expected %s, got %s", i, exp, list[i].JobID)
		}
	}
}

func TestCompareQueuedJobs_StrictOrderingProperties(t *testing.T) {
	cfg := DefaultAgingConfig()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Matrix of representative candidates
	priorities := []JobPriority{JobPriorityHigh, JobPriorityNormal, JobPriorityLow}
	positions := []int64{0, 1, 2, 5}
	waitOffsets := []time.Duration{0, 10 * time.Minute, 30 * time.Minute, 60 * time.Minute}

	var candidates []QueuedJob
	id := 1
	for _, p := range priorities {
		for _, pos := range positions {
			for _, w := range waitOffsets {
				candID := fmt.Sprintf("job-%03d-%s-pos%d-w%d", id, p, pos, int(w.Minutes()))
				candidates = append(candidates, QueuedJob{
					JobID:      candID,
					Position:   pos,
					EnqueuedAt: now.Add(-w),
					UpdatedAt:  now.Add(-w),
					Job: Job{
						ID:       candID,
						Priority: p,
					},
				})
				id++
			}
		}
	}

	n := len(candidates)
	for i := 0; i < n; i++ {
		a := &candidates[i]

		// 1. Irreflexivity: A < A is false
		if cmp := CompareQueuedJobs(a, a, now, cfg); cmp != 0 {
			t.Fatalf("irreflexivity violated for %s: cmp=%d", a.JobID, cmp)
		}

		for j := 0; j < n; j++ {
			b := &candidates[j]
			cmpAB := CompareQueuedJobs(a, b, now, cfg)
			cmpBA := CompareQueuedJobs(b, a, now, cfg)

			// 2. Asymmetry / Antisymmetry: cmp(A, B) == -cmp(B, A)
			if (cmpAB < 0 && cmpBA <= 0) || (cmpAB > 0 && cmpBA >= 0) {
				t.Fatalf("asymmetry violated between %s and %s: cmpAB=%d, cmpBA=%d", a.JobID, b.JobID, cmpAB, cmpBA)
			}
			if i != j && cmpAB == 0 {
				t.Fatalf("distinct jobs must not compare as equal: %s vs %s", a.JobID, b.JobID)
			}

			// 3. Transitivity
			if cmpAB < 0 {
				for k := 0; k < n; k++ {
					c := &candidates[k]
					cmpBC := CompareQueuedJobs(b, c, now, cfg)
					if cmpBC < 0 {
						// a < b and b < c => a < c
						cmpAC := CompareQueuedJobs(a, c, now, cfg)
						if cmpAC >= 0 {
							t.Fatalf("transitivity violated: %s < %s and %s < %s, but cmp(%s, %s)=%d",
								a.JobID, b.JobID, b.JobID, c.JobID, a.JobID, c.JobID, cmpAC)
						}
					}
				}
			}
		}
	}
}

func TestRankQueuedJobs_InputPermutationDeterminism(t *testing.T) {
	cfg := DefaultAgingConfig()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	items := []QueuedJob{
		{JobID: "j-run-now-1", Position: 0, EnqueuedAt: now.Add(-5 * time.Minute), UpdatedAt: now.Add(-5 * time.Minute), Job: Job{ID: "j-run-now-1", Priority: JobPriorityLow}},
		{JobID: "j-run-now-2", Position: 0, EnqueuedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute), Job: Job{ID: "j-run-now-2", Priority: JobPriorityHigh}},
		{JobID: "j-high-new", Position: 1, EnqueuedAt: now, UpdatedAt: now, Job: Job{ID: "j-high-new", Priority: JobPriorityHigh}},
		{JobID: "j-low-aged", Position: 2, EnqueuedAt: now.Add(-50 * time.Minute), UpdatedAt: now.Add(-50 * time.Minute), Job: Job{ID: "j-low-aged", Priority: JobPriorityLow}},
		{JobID: "j-norm-mid", Position: 1, EnqueuedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute), Job: Job{ID: "j-norm-mid", Priority: JobPriorityNormal}},
		{JobID: "j-norm-fresh", Position: 2, EnqueuedAt: now, UpdatedAt: now, Job: Job{ID: "j-norm-fresh", Priority: JobPriorityNormal}},
	}

	p1 := make([]QueuedJob, len(items))
	copy(p1, items)
	RankQueuedJobs(p1, now, cfg)

	var canonicalOrder []string
	for _, q := range p1 {
		canonicalOrder = append(canonicalOrder, q.JobID)
	}

	testPermutations := [][]int{
		{0, 1, 2, 3, 4, 5},
		{5, 4, 3, 2, 1, 0},
		{1, 2, 3, 4, 5, 0},
		{2, 3, 4, 5, 0, 1},
		{3, 4, 5, 0, 1, 2},
		{4, 5, 0, 1, 2, 3},
		{5, 0, 1, 2, 3, 4},
		{3, 1, 4, 5, 2, 0},
		{1, 5, 2, 4, 0, 3},
		{4, 2, 0, 5, 3, 1},
		{2, 0, 4, 1, 5, 3},
	}

	for pIdx, pIndices := range testPermutations {
		perm := make([]QueuedJob, len(items))
		for i, idx := range pIndices {
			perm[i] = items[idx]
		}
		RankQueuedJobs(perm, now, cfg)

		for idx, q := range perm {
			if q.JobID != canonicalOrder[idx] {
				t.Fatalf("permutation %d rank mismatch at %d: expected %s, got %s", pIdx, idx, canonicalOrder[idx], q.JobID)
			}
		}
	}
}

func TestCompareQueuedJobs_LowStarvationPrevented(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Low job queued at t0
	low := &QueuedJob{
		JobID:      "job-low-old",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-low-old", Priority: JobPriorityLow},
	}

	// 45 minutes later, a brand new High job arrives
	t45 := t0.Add(45 * time.Minute)
	newHigh := &QueuedJob{
		JobID:      "job-high-new",
		Position:   1,
		EnqueuedAt: t45,
		Job:        Job{ID: "job-high-new", Priority: JobPriorityHigh},
	}

	// At t45:
	// low score: base 0 + (45 min * 5 = 225) = 225
	// newHigh score: base 200 + (0 min * 5 = 0) = 200
	if effLow := EffectivePriority(low, t45, cfg); effLow != 225 {
		t.Fatalf("expected low effective score 225, got %d", effLow)
	}
	if effHigh := EffectivePriority(newHigh, t45, cfg); effHigh != 200 {
		t.Fatalf("expected new high effective score 200, got %d", effHigh)
	}

	// Low must beat newHigh!
	if cmp := CompareQueuedJobs(low, newHigh, t45, cfg); cmp >= 0 {
		t.Fatalf("expected aged low job to outrank newly arrived high job, got %d", cmp)
	}
}

func TestCompareQueuedJobs_RunNowPrecedence(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	runNowLow := &QueuedJob{
		JobID:      "job-run-now",
		Position:   0, // Run Now sentinel
		EnqueuedAt: t0,
		Job:        Job{ID: "job-run-now", Priority: JobPriorityLow},
	}
	highNormal := &QueuedJob{
		JobID:      "job-high",
		Position:   1,
		EnqueuedAt: t0.Add(-100 * time.Minute), // older high job
		Job:        Job{ID: "job-high", Priority: JobPriorityHigh},
	}

	// Run Now must always win regardless of base priority or wait time
	if cmp := CompareQueuedJobs(runNowLow, highNormal, t0, cfg); cmp >= 0 {
		t.Fatalf("expected Run Now job to outrank high priority non-run-now job, got %d", cmp)
	}
}

func TestCompareQueuedJobs_DeterministicTieBreaking(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jobA := &QueuedJob{
		JobID:      "job-aaa",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-aaa", Priority: JobPriorityNormal},
	}
	jobB := &QueuedJob{
		JobID:      "job-bbb",
		Position:   1,
		EnqueuedAt: t0,
		Job:        Job{ID: "job-bbb", Priority: JobPriorityNormal},
	}

	// Same priority, same position, same enqueue time -> job ID breaks tie deterministically
	cmp := CompareQueuedJobs(jobA, jobB, t0, cfg)
	if cmp >= 0 {
		t.Fatalf("expected job-aaa to outrank job-bbb on ID tie-breaker, got %d", cmp)
	}
	cmpRev := CompareQueuedJobs(jobB, jobA, t0, cfg)
	if cmpRev <= 0 {
		t.Fatalf("expected reverse comparison to be positive, got %d", cmpRev)
	}
}

func TestRankAndSelectQueuedJobs(t *testing.T) {
	cfg := DefaultAgingConfig()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jobs := []QueuedJob{
		{JobID: "low-1", Position: 1, EnqueuedAt: t0, Job: Job{ID: "low-1", Priority: JobPriorityLow}},
		{JobID: "high-1", Position: 1, EnqueuedAt: t0, Job: Job{ID: "high-1", Priority: JobPriorityHigh}},
		{JobID: "run-now", Position: 0, EnqueuedAt: t0, Job: Job{ID: "run-now", Priority: JobPriorityNormal}},
		{JobID: "norm-1", Position: 1, EnqueuedAt: t0, Job: Job{ID: "norm-1", Priority: JobPriorityNormal}},
	}

	best := SelectNextRunnable(jobs, t0, cfg)
	if best == nil || best.JobID != "run-now" {
		t.Fatalf("expected SelectNextRunnable to pick run-now, got %+v", best)
	}

	RankQueuedJobs(jobs, t0, cfg)
	expectedOrder := []string{"run-now", "high-1", "norm-1", "low-1"}
	for i, expectedID := range expectedOrder {
		if jobs[i].JobID != expectedID {
			t.Fatalf("rank position %d: expected %s, got %s", i, expectedID, jobs[i].JobID)
		}
	}
}
