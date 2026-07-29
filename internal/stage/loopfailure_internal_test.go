package stage

import "testing"

// The boot check that decides whether the loop-failure watcher can ever fire.
//
// EnsureStream is get-or-create and does not reconcile, so the AGENT stream this
// engine binds may have been created by semstreams' own provisioning (`agent.>`,
// derived from the stream name), by an operator's explicit config, or by this
// engine. Only the first and the last capture `agent.failed.*`, and getting this
// wrong in either direction is silent: too strict refuses the ordinary
// production deployment at boot, too loose binds a consumer that never receives
// anything and reports nothing.
func TestSubjectCovered_AcceptsEveryStreamThatCarriesLoopFailuresAndNoOthers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		subjects []string
		want     bool
	}{
		{
			name:     "semstreams' own derivation from the stream name",
			subjects: []string{"agent.>"},
			want:     true,
		},
		{
			name:     "an exact declaration of the failure subject",
			subjects: []string{"agent.failed.*"},
			want:     true,
		},
		{
			name:     "the failure subject among several",
			subjects: []string{"agent.task.*", "agent.failed.*", "agent.complete.*"},
			want:     true,
		},
		{
			name:     "a full wildcard over everything",
			subjects: []string{">"},
			want:     true,
		},
		{
			// The shape a test stand-in produced before this check existed: the
			// AGENT stream carried tasks and nothing else, so every loop failure
			// was published into no stream at all.
			name:     "tasks only",
			subjects: []string{"agent.task.*"},
			want:     false,
		},
		{
			name:     "a sibling namespace",
			subjects: []string{"agents.>"},
			want:     false,
		},
		{
			// One concrete loop's failures is not every loop's failures, and a
			// consumer filtered on the wildcard would receive nothing else.
			name:     "one concrete loop",
			subjects: []string{"agent.failed.loop-1"},
			want:     false,
		},
		{
			name:     "a prefix that stops short",
			subjects: []string{"agent.failed"},
			want:     false,
		},
		{
			name:     "no subjects at all",
			subjects: nil,
			want:     false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := subjectCovered(LoopFailedSubject, tc.subjects); got != tc.want {
				t.Fatalf("subjectCovered(%q, %v) = %v, want %v", LoopFailedSubject, tc.subjects, got, tc.want)
			}
		})
	}
}

// The stream this engine declares must satisfy its own check, or Start would
// refuse the stream it just created.
func TestAgentStreamConfig_CapturesTheSubjectTheWatcherBinds(t *testing.T) {
	if !subjectCovered(LoopFailedSubject, AgentStreamConfig().Subjects) {
		t.Fatalf("the AGENT stream this engine declares (%v) does not capture %q; Start would refuse the stream "+
			"it had just created", AgentStreamConfig().Subjects, LoopFailedSubject)
	}
	if !subjectCovered(TaskSubjectPrefix+"*", AgentStreamConfig().Subjects) {
		t.Fatalf("the AGENT stream this engine declares (%v) does not capture the task subject the spawner "+
			"publishes on", AgentStreamConfig().Subjects)
	}
}
