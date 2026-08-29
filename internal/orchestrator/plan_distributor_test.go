package orchestrator

import "testing"

func sumAlloc(alloc map[string]int32) int32 {
	total := int32(0)
	for _, v := range alloc {
		total += v
	}
	return total
}

// Per-stage allocations must sum to exactly the stage target, so the fleet
// reproduces the plan's load curve instead of drifting off it.
func TestAllocateStageVUsSumsToTarget(t *testing.T) {
	cases := []struct {
		name    string
		dist    map[string]int32
		targets []int32
	}{
		{
			name:    "equal capacity workers",
			dist:    map[string]int32{"w-a": 4, "w-b": 3, "w-c": 3},
			targets: []int32{0, 1, 2, 5, 7, 10},
		},
		{
			name:    "uneven capacity workers",
			dist:    map[string]int32{"big": 70, "mid": 20, "small": 10},
			targets: []int32{1, 7, 33, 100},
		},
		{
			name:    "single worker",
			dist:    map[string]int32{"only": 12},
			targets: []int32{1, 6, 12},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range tc.targets {
				alloc := allocateStageVUs(tc.dist, target)

				if got := sumAlloc(alloc); got != target {
					t.Errorf("target %d: allocations %v sum to %d, want %d", target, alloc, got, target)
				}
				for id, v := range alloc {
					if v > tc.dist[id] {
						t.Errorf("target %d: worker %s assigned %d, exceeds its %d VUs for the test",
							target, id, v, tc.dist[id])
					}
				}
			}
		})
	}
}

// Ties between equal remainders must break the same way every time, or workers
// would be handed a different split on each stage of the same test.
func TestAllocateStageVUsIsDeterministic(t *testing.T) {
	dist := map[string]int32{"w-a": 4, "w-b": 3, "w-c": 3}

	want := allocateStageVUs(dist, 5)
	for i := 0; i < 200; i++ {
		got := allocateStageVUs(dist, 5)
		for id := range want {
			if want[id] != got[id] {
				t.Fatalf("iteration %d: worker %s got %d, want %d", i, id, got[id], want[id])
			}
		}
	}
}

func TestAllocateStageVUsDegenerateInputs(t *testing.T) {
	if got := allocateStageVUs(nil, 5); len(got) != 0 {
		t.Errorf("nil distribution: got %v, want empty", got)
	}
	if got := allocateStageVUs(map[string]int32{"w": 3}, 0); len(got) != 0 {
		t.Errorf("zero target: got %v, want empty", got)
	}
	if got := allocateStageVUs(map[string]int32{"w": 0}, 5); got["w"] != 0 {
		t.Errorf("zero-share worker: got %v, want 0", got)
	}
}
