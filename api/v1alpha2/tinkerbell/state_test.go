package tinkerbell

import (
	"encoding/json"
	"testing"
)

func TestStateJSON(t *testing.T) {
	b, err := json.Marshal(Metadata{State: StatePending})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"state":"Pending"}` {
		t.Errorf("marshal = %s, want the state's name", b)
	}
	if b, _ := json.Marshal(Metadata{}); string(b) != `{}` {
		t.Errorf("an unset state marshals to %s, want it omitted", b)
	}

	for in, want := range map[string]State{
		`"Running"`: StateRunning,
		`"SUCCESS"`: StateSuccess, // a v1alpha1 name
		`"timeout"`: StateTimeout,
		`5`:         StateSuccess, // stored by Tinkerbell v0.27
		`""`:        0,
	} {
		var s State
		if err := json.Unmarshal([]byte(in), &s); err != nil {
			t.Errorf("unmarshal %s: %v", in, err)
			continue
		}
		if s != want {
			t.Errorf("unmarshal %s = %v, want %v", in, s, want)
		}
	}
	var s State
	if err := json.Unmarshal([]byte(`"Bogus"`), &s); err == nil {
		t.Error("an unknown name decoded")
	}
}
