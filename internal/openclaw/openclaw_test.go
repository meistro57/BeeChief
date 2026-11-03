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

const sampleSkillListFixture = `{
  "workspaceDir": "/srv/oc/workspace",
  "managedSkillsDir": "/srv/oc/skills",
  "skills": [
    {
      "name": "github",
      "description": "Manage GitHub issues and PRs.",
      "emoji": "🐙",
      "eligible": true,
      "disabled": false,
      "modelVisible": true,
      "userInvocable": true,
      "commandVisible": true,
      "source": "openclaw-bundled",
      "bundled": true,
      "missing": {"bins": [], "anyBins": [], "env": [], "config": [], "os": []}
    },
    {
      "name": "1password",
      "description": "Use 1Password CLI.",
      "emoji": "🔐",
      "eligible": false,
      "disabled": false,
      "modelVisible": false,
      "source": "openclaw-bundled",
      "missing": {"bins": ["op"], "anyBins": [], "env": [], "config": [], "os": []}
    },
    {
      "name": "disabled-skill",
      "description": "Intentionally disabled.",
      "eligible": false,
      "disabled": true,
      "missing": {"bins": [], "anyBins": [], "env": [], "config": [], "os": []}
    }
  ]
}`

const sampleSkillCheckFixture = `{
  "agentId": "alpha",
  "workspaceDir": "/srv/oc/workspace",
  "managedSkillsDir": "/srv/oc/skills",
  "summary": {
    "total": 3,
    "eligible": 1,
    "modelVisible": 1,
    "commandVisible": 1,
    "disabled": 1,
    "blocked": 0,
    "missingRequirements": 1
  },
  "eligible": ["github"],
  "disabled": ["disabled-skill"],
  "missingRequirements": [
    {
      "name": "1password",
      "missing": {"bins": ["op"]}
    }
  ]
}`

const sampleWorkshopListFixture = `{
  "agentId": "alpha",
  "mode": "auto",
  "root": "/srv/oc/agents/alpha/agent/workshop-skills",
  "skills": [
    {
      "name": "deploy-runbook",
      "description": "Verify staging health before rolling deploy.",
      "files": ["SKILL.md"],
      "sizeBytes": 912,
      "updatedAtMs": 1790602881820,
      "useCount": 3
    }
  ],
  "archived": []
}`

const sampleModelsStatusFixture = `{
  "configPath": "/srv/oc/openclaw.json",
  "agentDir": "/srv/oc/agents/alpha/agent",
  "defaultModel": "ollama/llama3:8b",
  "resolvedDefault": "ollama/llama3:8b",
  "fallbacks": ["openrouter/auto"],
  "allowed": ["ollama/llama3:8b", "openrouter/auto"],
  "auth": {
    "missingProvidersInUse": ["anthropic"],
    "modelRouteIssues": ["ollama model missing"],
    "providers": [
      {
        "provider": "ollama",
        "effective": {"kind": "profiles", "detail": "local"}
      }
    ]
  }
}`

func TestListAndCheckSkillsFixtures(t *testing.T) {
	f := &fakeExec{res: hosts.Result{Stdout: sampleSkillListFixture}}
	rep, err := NewClient(f).ListSkills(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if !reflect.DeepEqual(f.args, []string{"skills", "list", "--agent", "alpha", "--json"}) {
		t.Errorf("unexpected args: %v", f.args)
	}
	if len(rep.Skills) != 3 {
		t.Fatalf("skills count: got %d, want 3", len(rep.Skills))
	}
	if got := len(rep.HealthySkills()); got != 1 || rep.HealthySkills()[0].Name != "github" {
		t.Errorf("HealthySkills: %+v", rep.HealthySkills())
	}
	if got := len(rep.ProblemSkills()); got != 1 || rep.ProblemSkills()[0].Name != "1password" {
		t.Errorf("ProblemSkills: %+v", rep.ProblemSkills())
	}
	if rep.RawOutput == "" {
		t.Error("expected RawOutput to be preserved")
	}

	f.res = hosts.Result{Stdout: sampleSkillCheckFixture}
	chk, err := NewClient(f).CheckSkills(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("CheckSkills: %v", err)
	}
	if chk.Summary.Total != 3 || chk.Summary.Eligible != 1 || chk.Summary.MissingRequirements != 1 {
		t.Errorf("unexpected check summary: %+v", chk.Summary)
	}
	if len(chk.MissingRequirements) != 1 || chk.MissingRequirements[0].Missing.Summary() != "bins: op" {
		t.Errorf("unexpected missing requirements: %+v", chk.MissingRequirements)
	}
}

func TestUnsupportedVersionSurfacedClearly(t *testing.T) {
	f := &fakeExec{res: hosts.Result{ExitCode: 1, Stderr: "error: unknown command 'workshop'"}}
	_, err := NewClient(f).ListWorkshop(context.Background(), "alpha")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestWorkshopAndSelfReview(t *testing.T) {
	f := &fakeExec{res: hosts.Result{Stdout: sampleWorkshopListFixture}}
	ws, err := NewClient(f).ListWorkshop(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ListWorkshop: %v", err)
	}
	if len(ws.Proposals) != 1 || ws.Proposals[0].Name != "deploy-runbook" {
		t.Errorf("unexpected proposals: %+v", ws.Proposals)
	}

	f.res = hosts.Result{Stdout: `{"name":"deploy-runbook","filePath":"SKILL.md","content":"# Deploy Runbook\nStep 1"}`}
	prop, err := NewClient(f).ShowWorkshopProposal(context.Background(), "alpha", "deploy-runbook")
	if err != nil {
		t.Fatalf("ShowWorkshopProposal: %v", err)
	}
	if prop.Content != "# Deploy Runbook\nStep 1" {
		t.Errorf("unexpected proposal content: %q", prop.Content)
	}

	f.res = hosts.Result{Stdout: `{"reply":"Inspected 3 skills; proposed deploy-runbook improvement."}`}
	sr, err := NewClient(f).SelfReview(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("SelfReview: %v", err)
	}
	if sr.Summary != "Inspected 3 skills; proposed deploy-runbook improvement." {
		t.Errorf("unexpected review summary: %q", sr.Summary)
	}
}

func TestParseModelsStatusFixture(t *testing.T) {
	f := &fakeExec{res: hosts.Result{Stdout: sampleModelsStatusFixture}}
	ms, err := NewClient(f).ModelsStatus(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ModelsStatus: %v", err)
	}
	if ms.DefaultModel != "ollama/llama3:8b" || len(ms.MissingProvidersInUse) != 1 || len(ms.Providers) != 1 {
		t.Errorf("unexpected model status: %+v", ms)
	}
}
