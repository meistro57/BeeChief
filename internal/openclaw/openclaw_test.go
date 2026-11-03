package openclaw

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/meistro57/beechief/internal/hosts"
)

// sampleList is shaped like real `openclaw agents list --json` output.
const sampleList = `[
  {
    "id": "alpha",
    "identityName": "Alpha",
    "identityEmoji": "🤖",
    "workspace": "/srv/oc/workspace",
    "agentDir": "/srv/oc/agents/alpha/agent",
    "model": "provider/model-a",
    "bindings": 2,
    "isDefault": true
  },
  {
    "id": "beta",
    "name": "beta",
    "workspace": "/srv/oc/workspace-beta",
    "model": "provider/model-b",
    "isDefault": false,
    "identityAvatarUrl": "data:image/webp;base64,AAAA",
    "createdAt": 1788750356055
  }
]`

func TestParseAgentsArray(t *testing.T) {
	bees, err := ParseAgents([]byte(sampleList))
	if err != nil {
		t.Fatalf("ParseAgents: %v", err)
	}
	if len(bees) != 2 {
		t.Fatalf("got %d bees, want 2", len(bees))
	}
	a := bees[0]
	if a.ID != "alpha" || a.Name != "Alpha" || a.Emoji != "🤖" || a.Runtime != "provider/model-a" ||
		a.Workspace != "/srv/oc/workspace" || a.AgentDir != "/srv/oc/agents/alpha/agent" || !a.Default {
		t.Errorf("alpha parsed wrong: %+v", a)
	}
	if b := bees[1]; b.ID != "beta" || b.Name != "beta" || b.Default || b.Hive != "" {
		t.Errorf("beta parsed wrong: %+v", b)
	}
}

func TestParseAgentsWrappedObjectAndFallbacks(t *testing.T) {
	doc := `{"agents":[{"name":"onlyname","status":"running"},{"workspace":"/x"},{"id":"withid"}]}`
	bees, err := ParseAgents([]byte(doc))
	if err != nil {
		t.Fatalf("ParseAgents: %v", err)
	}
	got := []string{}
	for _, b := range bees {
		got = append(got, b.ID+"|"+b.Name+"|"+b.Status)
	}
	want := []string{"onlyname|onlyname|running", "withid|withid|"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseAgentsSkipsLeadingNoise(t *testing.T) {
	doc := "Config warnings:\n- something [deprecated]\n" + sampleList + "\n"
	bees, err := ParseAgents([]byte(doc))
	if err != nil {
		t.Fatalf("ParseAgents: %v", err)
	}
	if len(bees) != 2 {
		t.Errorf("got %d bees, want 2", len(bees))
	}
}

func TestParseAgentsEmptyList(t *testing.T) {
	bees, err := ParseAgents([]byte("[]"))
	if err != nil || len(bees) != 0 {
		t.Fatalf("want empty list, got %v, %v", bees, err)
	}
}

func TestParseAgentsInvalid(t *testing.T) {
	for _, doc := range []string{"", "not json", "{\"agents\": 5}", `{"other":[]}`, "[{\"id\": ", `"string"`} {
		if _, err := ParseAgents([]byte(doc)); !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("ParseAgents(%q): want ErrInvalidOutput, got %v", doc, err)
		}
	}
}

// fakeExec is a scripted hosts.Execution.
type fakeExec struct {
	res     hosts.Result
	err     error
	command string
	args    []string
}

func (f *fakeExec) Run(_ context.Context, command string, args ...string) (hosts.Result, error) {
	f.command, f.args = command, args
	return f.res, f.err
}

func TestDiscoverAgentsRunsJSONListing(t *testing.T) {
	f := &fakeExec{res: hosts.Result{Stdout: sampleList}}
	bees, err := NewClient(f).DiscoverAgents(context.Background())
	if err != nil {
		t.Fatalf("DiscoverAgents: %v", err)
	}
	if f.command != "openclaw" || !reflect.DeepEqual(f.args, []string{"agents", "list", "--json"}) {
		t.Errorf("ran %q %q", f.command, f.args)
	}
	if len(bees) != 2 {
		t.Errorf("got %d bees", len(bees))
	}
}

func TestDiscoverAgentsErrors(t *testing.T) {
	unreachable := &hosts.Error{Hive: "h", Kind: hosts.KindUnreachable, Command: "ssh"}
	cases := []struct {
		name  string
		exec  *fakeExec
		check func(error) bool
	}{
		{"missing local", &fakeExec{err: &hosts.Error{Kind: hosts.KindNotFound}},
			func(e error) bool { return errors.Is(e, ErrNotInstalled) }},
		{"missing remote", &fakeExec{res: hosts.Result{ExitCode: 127, Stderr: "sh: openclaw: not found"}},
			func(e error) bool { return errors.Is(e, ErrNotInstalled) }},
		{"command failed", &fakeExec{res: hosts.Result{ExitCode: 1, Stderr: "boom"}},
			func(e error) bool { return errors.Is(e, ErrCommandFailed) }},
		{"invalid json", &fakeExec{res: hosts.Result{Stdout: "Usage: openclaw ..."}},
			func(e error) bool { return errors.Is(e, ErrInvalidOutput) }},
		{"unreachable", &fakeExec{err: unreachable},
			func(e error) bool { return hosts.IsKind(e, hosts.KindUnreachable) }},
	}
	for _, c := range cases {
		bees, err := NewClient(c.exec).DiscoverAgents(context.Background())
		if err == nil || !c.check(err) {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if bees != nil {
			t.Errorf("%s: want nil bees, got %v", c.name, bees)
		}
	}
}
