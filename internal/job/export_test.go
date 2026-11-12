package job

// AddActiveForTest registers a copy of j as an active job, exactly as a successful dispatch does. Engine
// observations (monitor ticks) apply only to active jobs, so external-package tests use this to model a running job.
func (m *Manager) AddActiveForTest(j *Job) {
	c := *j
	m.addActive(&c)
}
