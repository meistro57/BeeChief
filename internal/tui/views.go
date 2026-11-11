package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
	"github.com/meistro57/beechief/internal/swarm"
)

func (m *Model) syncSelectionAfterScan() {
	all := m.inventory.AllBees()
	if m.selectedBee == nil && len(all) > 0 {
		b := all[0]
		m.selectedBee = &b
	} else if m.selectedBee != nil {
		for _, b := range all {
			if b.Key() == m.selectedBee.Key() {
				bCopy := b
				m.selectedBee = &bCopy
				break
			}
		}
	}
	if m.hiveIdx >= len(m.inventory.Hives) {
		m.hiveIdx = 0
	}
}

func (m Model) filteredBees() []agents.Bee {
	return m.inventory.FilterBees(m.filterQuery)
}

func (m Model) filteredSkills() []openclaw.Skill {
	if m.skillList == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(m.skillFilterQuery))
	if q == "" {
		return m.skillList.Skills
	}
	var out []openclaw.Skill
	for _, sk := range m.skillList.Skills {
		state := strings.ToLower(sk.StateLabel())
		if strings.Contains(strings.ToLower(sk.Name), q) ||
			strings.Contains(strings.ToLower(sk.Description), q) ||
			strings.Contains(strings.ToLower(sk.Source), q) ||
			strings.Contains(state, q) {
			out = append(out, sk)
		}
	}
	return out
}

func (m Model) listViewportRows() int {
	if m.height <= 0 {
		return 18
	}
	rows := m.height - 14
	if rows < 4 {
		rows = 4
	}
	if rows > 35 {
		rows = 35
	}
	return rows
}

func visibleSliceBounds(total, cursor, maxRows int) (start, end int) {
	if total <= 0 {
		return 0, 0
	}
	if maxRows <= 0 || total <= maxRows {
		return 0, total
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	half := maxRows / 2
	start = cursor - half
	if start < 0 {
		start = 0
	}
	end = start + maxRows
	if end > total {
		end = total
		start = end - maxRows
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func (m Model) renderTitle(s string) string {
	if m.noColor {
		return s
	}
	return titleStyle.Render(s)
}

func (m Model) renderTagline(s string) string {
	if m.noColor {
		return s
	}
	return taglineStyle.Render(s)
}

func (m Model) renderHeader(s string) string {
	if m.noColor {
		return s
	}
	return headerStyle.Render(s)
}

func (m Model) renderHelp(s string) string {
	if m.noColor {
		return s
	}
	return helpStyle.Render(s)
}

func (m Model) renderCursor(s string) string {
	if m.noColor {
		return s
	}
	return cursorStyle.Render(s)
}

func (m Model) renderOK(s string) string {
	if m.noColor {
		return s
	}
	return okStyle.Render(s)
}

func (m Model) renderWarn(s string) string {
	if m.noColor {
		return s
	}
	return warnStyle.Render(s)
}

func (m Model) renderFail(s string) string {
	if m.noColor {
		return s
	}
	return failStyle.Render(s)
}

func (m *Model) activeBee() *agents.Bee {
	if m.selectedBee != nil {
		return m.selectedBee
	}
	all := m.inventory.AllBees()
	if len(all) > 0 {
		b := all[0]
		m.selectedBee = &b
		return m.selectedBee
	}
	return nil
}

func (m *Model) cycleActiveBee(forward bool) {
	all := m.inventory.AllBees()
	if len(all) == 0 {
		return
	}
	curIdx := 0
	if m.selectedBee != nil {
		for i, b := range all {
			if b.Key() == m.selectedBee.Key() {
				curIdx = i
				break
			}
		}
	}
	if forward {
		curIdx = (curIdx + 1) % len(all)
	} else {
		curIdx = (curIdx - 1 + len(all)) % len(all)
	}
	b := all[curIdx]
	m.selectedBee = &b
	m.busy = false
}

func (m Model) activeHive() string {
	if len(m.inventory.Hives) > 0 {
		if m.hiveIdx >= 0 && m.hiveIdx < len(m.inventory.Hives) {
			return m.inventory.Hives[m.hiveIdx]
		}
		return m.inventory.Hives[0]
	}
	if len(m.cfg.Hives) > 0 {
		if m.hiveIdx >= 0 && m.hiveIdx < len(m.cfg.Hives) {
			return m.cfg.Hives[m.hiveIdx].Name
		}
		return m.cfg.Hives[0].Name
	}
	return ""
}

func (m *Model) cycleActiveHive(forward bool) {
	n := len(m.inventory.Hives)
	if n == 0 {
		n = len(m.cfg.Hives)
	}
	if n == 0 {
		return
	}
	if forward {
		m.hiveIdx = (m.hiveIdx + 1) % n
	} else {
		m.hiveIdx = (m.hiveIdx - 1 + n) % n
	}
	m.busy = false
}

func (m Model) currentServicesCount() int {
	if m.servicesReport == nil {
		return 0
	}
	switch m.servicesTab {
	case 0:
		return len(m.servicesReport.Systemd.Units)
	case 1:
		return len(m.servicesReport.Docker.Containers)
	case 2:
		return len(m.servicesReport.Models.OllamaModels)
	}
	return 0
}

func (m *Model) loadSkillsCmd() tea.Cmd {
	bee := m.activeBee()
	if m.ops == nil || bee == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	target := *bee
	beeKey := target.Key()
	return func() tea.Msg {
		rep, err := ops.ListSkills(context.Background(), target)
		return skillListMsg{target: beeKey, rep: rep, err: err}
	}
}

func (m *Model) checkSkillsCmd() tea.Cmd {
	bee := m.activeBee()
	if m.ops == nil || bee == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	target := *bee
	beeKey := target.Key()
	return func() tea.Msg {
		rep, err := ops.CheckSkills(context.Background(), target)
		return skillCheckMsg{target: beeKey, rep: rep, err: err}
	}
}

func (m *Model) selfReviewCmd() tea.Cmd {
	bee := m.activeBee()
	if m.ops == nil || bee == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	target := *bee
	beeKey := target.Key()
	return func() tea.Msg {
		rep, err := ops.SelfReview(context.Background(), target)
		return selfReviewMsg{target: beeKey, rep: rep, err: err}
	}
}

func (m *Model) loadWorkshopCmd() tea.Cmd {
	bee := m.activeBee()
	if m.ops == nil || bee == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	target := *bee
	beeKey := target.Key()
	return func() tea.Msg {
		rep, err := ops.ListWorkshop(context.Background(), target)
		return workshopListMsg{target: beeKey, rep: rep, err: err}
	}
}

func (m *Model) showProposalCmd(name string) tea.Cmd {
	bee := m.activeBee()
	if m.ops == nil || bee == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	target := *bee
	beeKey := target.Key()
	return func() tea.Msg {
		prop, err := ops.ShowWorkshopProposal(context.Background(), target, name)
		return workshopShowMsg{target: beeKey, prop: prop, err: err}
	}
}

func (m *Model) loadModelStatusCmd() tea.Cmd {
	bee := m.activeBee()
	if m.ops == nil || bee == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	target := *bee
	beeKey := target.Key()
	return func() tea.Msg {
		rep, err := ops.ModelsStatus(context.Background(), target)
		return modelStatusMsg{target: beeKey, rep: rep, err: err}
	}
}

func (m *Model) runHiveHealthCmd() tea.Cmd {
	hive := m.activeHive()
	if m.ops == nil || hive == "" {
		return nil
	}
	m.busy = true
	ops := m.ops
	return func() tea.Msg {
		rep, err := ops.RunHiveHealth(context.Background(), hive)
		return hiveHealthMsg{target: hive, rep: rep, err: err}
	}
}

func (m *Model) inspectServicesCmd() tea.Cmd {
	hive := m.activeHive()
	if m.ops == nil || hive == "" {
		return nil
	}
	m.busy = true
	ops := m.ops
	return func() tea.Msg {
		rep, err := ops.InspectServices(context.Background(), hive)
		return servicesMsg{target: hive, rep: rep, err: err}
	}
}

func (m *Model) inspectUnitCmd(unit string) tea.Cmd {
	hive := m.activeHive()
	if m.ops == nil || hive == "" {
		return nil
	}
	m.busy = true
	ops := m.ops
	return func() tea.Msg {
		u, err := ops.InspectSystemdUnit(context.Background(), hive, unit)
		return unitDetailMsg{target: hive, unit: u, err: err}
	}
}

func (m *Model) controlServiceCmd(unit string, action services.ServiceAction, confirmed bool) tea.Cmd {
	hive := m.activeHive()
	if m.ops == nil || hive == "" {
		return nil
	}
	m.busy = true
	ops := m.ops
	return func() tea.Msg {
		err := ops.ControlSystemdService(context.Background(), hive, unit, action, confirmed)
		return serviceControlMsg{target: hive, unit: unit, action: action, err: err}
	}
}

func (m *Model) fetchDockerLogsCmd(container string) tea.Cmd {
	hive := m.activeHive()
	if m.ops == nil || hive == "" {
		return nil
	}
	m.busy = true
	ops := m.ops
	return func() tea.Msg {
		out, err := ops.FetchDockerLogs(context.Background(), hive, container, 40)
		return dockerLogsMsg{target: hive, container: container, logs: out, err: err}
	}
}

func (m *Model) fetchLogsCmd() tea.Cmd {
	hive := m.activeHive()
	if m.ops == nil || hive == "" {
		return nil
	}
	m.busy = true
	ops := m.ops
	kind := logs.AllSources[m.logSourceIdx]
	target := m.logTarget
	return func() tea.Msg {
		res, err := ops.FetchLogs(context.Background(), hive, kind, target, logs.DefaultTail)
		return logsResultMsg{target: hive, source: kind, res: res, err: err}
	}
}

func (m *Model) exportHoneyReportCmd() tea.Cmd {
	if m.ops == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	inv := m.inventory.Clone()
	return func() tea.Msg {
		path, err := ops.ExportHoneyReport(context.Background(), inv, "")
		return honeyExportMsg{path: path, err: err}
	}
}

func (m *Model) testHiveCmd(h config.Hive) tea.Cmd {
	if m.ops == nil {
		return nil
	}
	m.busy = true
	ops := m.ops
	return func() tea.Msg {
		return hiveTestMsg{res: ops.TestHive(context.Background(), h)}
	}
}

func (m Model) startSwarmReview(mode swarm.Mode) (tea.Model, tea.Cmd) {
	if m.swarmCoord != nil && (m.swarmSnap == nil || !m.swarmSnap.Done) {
		m.statusBanner = "Swarm Review already running; press x to cancel"
		return m, nil
	}
	bees := swarm.SelectBees(m.inventory, mode, m.activeBee(), m.activeHive())
	if len(bees) == 0 {
		m.statusBanner = "No Bees available for Swarm Review in mode " + string(mode)
		return m, nil
	}
	if m.ops == nil {
		coord := swarm.NewCoordinator(mode, bees, nil, swarm.DefaultConcurrency, nil)
		snap := coord.Snapshot()
		m.swarmSnap = &snap
		m.swarmStream = nil
		m.busy = false
		return m, nil
	}
	m.swarmRunID++
	runID := m.swarmRunID
	stream := newSwarmStream(runID, len(bees)*2+8)
	coord := m.ops.StartSwarmReview(mode, bees, stream.send)
	snap := coord.Snapshot()
	m.swarmCoord = coord
	m.swarmSnap = &snap
	m.swarmStream = stream
	m.busy = true
	m.statusBanner = fmt.Sprintf("Running Swarm Review (%s) across %d Bee(s)…", mode, len(bees))
	return m, tea.Batch(
		func() tea.Msg {
			final := coord.Run(context.Background())
			stream.close()
			return swarmProgressMsg{runID: runID, snap: final}
		},
		stream.wait(),
	)
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var out string
	switch m.screen {
	case screenSwarmOverview:
		out = m.viewSwarmOverview()
	case screenBees:
		out = m.viewBees()
	case screenBeeDetail:
		out = m.viewBeeDetail()
	case screenComb:
		out = m.viewComb()
	case screenSelfReview:
		out = m.viewSelfReview()
	case screenTroubleshooting:
		out = m.viewTroubleshooting()
	case screenServices:
		out = m.viewServices()
	case screenLogs:
		out = m.viewLogs()
	case screenSettings:
		out = m.viewSettings()
	default:
		out = m.viewRoot()
	}
	if m.width > 0 && m.height > 0 && (m.width < 50 || m.height < 12) {
		notice := m.renderWarn(fmt.Sprintf("[Compact terminal %dx%d — resize to ≥60x16 for full view]", m.width, m.height))
		out = notice + "\n" + out
	}
	return out
}

func (m Model) viewRoot() string {
	var b strings.Builder
	b.WriteString(m.renderTitle("⬢ BeeChief 👑🐝"))
	b.WriteString("  ")
	b.WriteString(m.renderTagline("THE SWARM COMMAND CENTER"))
	b.WriteString("\n")
	b.WriteString(m.renderHelp("One terminal. Every hive. Discover, review & diagnose your agents."))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Bees discovered: %d\n", m.inventory.Total()))
	b.WriteString(m.hiveSummary())
	b.WriteString("\n")

	for i, s := range m.sections {
		if i == m.cursor {
			b.WriteString(m.renderCursor("> " + s.Title))
			b.WriteString("  " + m.renderHelp("— "+s.Blurb))
		} else {
			b.WriteString(plainStyle.Render("  " + s.Title))
		}
		b.WriteString("\n")
	}

	if m.statusBanner != "" {
		b.WriteString("\n")
		b.WriteString(m.renderWarn(m.statusBanner))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.renderHelp("↑/↓ move · enter select · r rescan · q quit"))
	return b.String()
}

// hiveSummary renders one line per Hive, marking failed Hives visibly.
func (m Model) hiveSummary() string {
	var b strings.Builder
	if m.scanning {
		b.WriteString(m.renderHelp("Scanning hives…"))
		b.WriteString("\n")
	}
	for _, h := range m.inventory.Hives {
		st := m.inventory.Status[h]
		if st.OK() {
			b.WriteString(m.renderOK(fmt.Sprintf("  ✓ %s: %d bees", h, len(m.inventory.ByHive[h]))))
		} else {
			b.WriteString(m.renderFail(fmt.Sprintf("  ✗ %s: %v", h, st.Err)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) viewSwarmOverview() string {
	var b strings.Builder
	b.WriteString(m.renderTitle("BeeChief 🐝 — Swarm Overview"))
	b.WriteString("\n")

	lastScan := "never"
	if t := m.inventory.LastScanTime(); !t.IsZero() {
		lastScan = t.Format("15:04:05")
	}
	scanState := "idle"
	if m.scanning {
		scanState = "scanning…"
	}
	fmt.Fprintf(&b, "Total Hives: %d · Total Bees: %d · Last scan: %s (%s)\n\n",
		len(m.inventory.Hives), m.inventory.Total(), lastScan, scanState)

	b.WriteString(m.renderHeader("Hives"))
	b.WriteString("\n")
	if len(m.inventory.Hives) == 0 {
		b.WriteString("  (no Hives configured — open Settings to add one)\n")
	}
	for _, h := range m.inventory.Hives {
		st := m.inventory.Status[h]
		if st.OK() {
			b.WriteString(m.renderOK(fmt.Sprintf("  ✓ %s [online] — %d bee(s)", h, len(m.inventory.ByHive[h]))))
		} else {
			b.WriteString(m.renderFail(fmt.Sprintf("  ✗ %s [offline] — %v", h, st.Err)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	m.renderBeeList(&b)
	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(m.renderHelp("↑/↓ move · enter Bee details · / filter · e export Honey Report · r rescan · esc back · q quit"))
	return b.String()
}

func (m Model) viewBees() string {
	var b strings.Builder
	b.WriteString(m.renderTitle("BeeChief 🐝 — Discovered Bees"))
	b.WriteString("\n\n")
	m.renderBeeList(&b)
	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(m.renderHelp("↑/↓ move · enter Bee details · / filter · r rescan · esc back · q quit"))
	return b.String()
}

func (m Model) renderBeeList(b *strings.Builder) {
	bees := m.filteredBees()
	maxRows := m.listViewportRows()
	start, end := visibleSliceBounds(len(bees), m.beeCursor, maxRows)

	header := "Discovered Bees"
	if m.filterQuery != "" {
		header += fmt.Sprintf(" (filter: %q)", m.filterQuery)
	}
	if len(bees) > maxRows {
		header += fmt.Sprintf(" [showing %d–%d of %d]", start+1, end, len(bees))
	}
	b.WriteString(m.renderHeader(header))
	b.WriteString("\n")

	if len(bees) == 0 {
		b.WriteString("  (no matching Bees discovered)\n")
		return
	}
	for i := start; i < end; i++ {
		bee := bees[i]
		emoji := bee.Emoji
		if emoji == "" {
			emoji = "🐝"
		}
		model := firstOr(bee.Runtime, "default-model")
		defTag := ""
		if bee.Default {
			defTag = " [default]"
		}
		line := fmt.Sprintf("%s %s (%s) — Hive: %s · Model: %s%s",
			emoji, bee.DisplayName(), bee.ID, bee.Hive, model, defTag)
		if i == m.beeCursor {
			b.WriteString(m.renderCursor("> " + line))
		} else {
			b.WriteString(plainStyle.Render("  " + line))
		}
		b.WriteString("\n")
	}
}

func (m Model) viewBeeDetail() string {
	var b strings.Builder
	bee := m.activeBee()
	if bee == nil {
		b.WriteString(titleStyle.Render("BeeChief 🐝 — Bee Detail"))
		b.WriteString("\n\nNo Bee selected.\n\n")
		b.WriteString(helpStyle.Render("esc back"))
		return b.String()
	}

	emoji := firstOr(bee.Emoji, "🐝")
	b.WriteString(titleStyle.Render(fmt.Sprintf("BeeChief 🐝 — %s %s (%s)", emoji, bee.DisplayName(), bee.Key())))
	b.WriteString("\n")
	st := m.inventory.Status[bee.Hive]
	hiveState := "online"
	if !st.OK() {
		hiveState = fmt.Sprintf("offline: %v", st.Err)
	}
	fmt.Fprintf(&b, "Hive: %s (%s) · ID: %s · Default: %v\n", bee.Hive, hiveState, bee.ID, bee.Default)
	fmt.Fprintf(&b, "Model: %s · Workspace: %s\n", firstOr(bee.Runtime, "unspecified"), firstOr(bee.Workspace, "unspecified"))
	if bee.AgentDir != "" {
		fmt.Fprintf(&b, "AgentDir: %s\n", bee.AgentDir)
	}
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("Actions"))
	b.WriteString("\n")
	for i, act := range BeeDetailActions {
		if i == m.detailCursor {
			b.WriteString(cursorStyle.Render("> " + act))
		} else {
			b.WriteString(plainStyle.Render("  " + act))
		}
		b.WriteString("\n")
	}

	if m.detailOutput != "" {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("Output"))
		b.WriteString("\n")
		b.WriteString(m.detailOutput)
		b.WriteString("\n")
	}
	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ move · enter run action · r rescan · esc back"))
	return b.String()
}

func (m Model) viewComb() string {
	var b strings.Builder
	bee := m.activeBee()
	beeLabel := "none"
	if bee != nil {
		beeLabel = fmt.Sprintf("%s (%s)", bee.DisplayName(), bee.Key())
	}
	b.WriteString(m.renderTitle("BeeChief 🐝 — Comb / Skills Inspection"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Selected Bee: %s\n\n", beeLabel)

	if m.skillCheck != nil {
		s := m.skillCheck.Summary
		fmt.Fprintf(&b, "Skill Check Summary: %d total · %s · %s · %d disabled\n\n",
			s.Total,
			m.renderOK(fmt.Sprintf("%d loadable/healthy", s.Eligible)),
			m.renderWarn(fmt.Sprintf("%d missing requirements", s.MissingRequirements)),
			s.Disabled,
		)
	}

	if m.skillList == nil {
		b.WriteString("No skill list loaded yet. Press 'l' to list skills or 'c' to run skill check.\n")
	} else {
		healthy := m.skillList.HealthySkills()
		problems := m.skillList.ProblemSkills()
		skills := m.filteredSkills()
		maxRows := m.listViewportRows()
		start, end := visibleSliceBounds(len(skills), m.skillCursor, maxRows)

		fmt.Fprintf(&b, "Discovered: %d · Loadable/Healthy: %d · Broken/Problem: %d",
			len(m.skillList.Skills), len(healthy), len(problems))
		if m.skillFilterQuery != "" {
			fmt.Fprintf(&b, " · Filter: %q (%d match)", m.skillFilterQuery, len(skills))
		}
		if len(skills) > maxRows {
			fmt.Fprintf(&b, " [showing %d–%d of %d]", start+1, end, len(skills))
		}
		b.WriteString("\n\n")

		for i := start; i < end; i++ {
			sk := skills[i]
			badge := m.renderOK("[ready]")
			if sk.Broken() {
				badge = m.renderFail("[" + sk.StateLabel() + "]")
			} else if sk.Disabled {
				badge = m.renderHelp("[disabled]")
			}
			line := fmt.Sprintf("%s %s — %s", sk.Name, badge, sk.Description)
			if i == m.skillCursor {
				b.WriteString(m.renderCursor("> ") + line)
			} else {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}

		if m.skillCursor >= 0 && m.skillCursor < len(skills) {
			sel := skills[m.skillCursor]
			b.WriteString("\n")
			b.WriteString(m.renderHeader("Selected Skill Diagnostics: " + sel.Name))
			b.WriteString("\n")
			fmt.Fprintf(&b, "  Source: %s · Eligible: %v · ModelVisible: %v · CommandVisible: %v\n",
				firstOr(sel.Source, "unknown"), sel.Eligible, sel.ModelVisible, sel.CommandVisible)
			if sel.Missing.HasMissing() {
				fmt.Fprintf(&b, "  Missing Requirements: %s\n", sel.Missing.Summary())
			}
		}

		if m.showRawComb {
			b.WriteString("\n")
			b.WriteString(m.renderHeader("Raw OpenClaw Output"))
			b.WriteString("\n")
			raw := m.skillList.RawOutput
			if m.skillCheck != nil && m.skillCheck.RawOutput != "" {
				raw = m.skillCheck.RawOutput
			}
			b.WriteString(truncateText(raw, 800))
			b.WriteString("\n")
		}
	}

	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(m.renderHelp("↑/↓ select skill · / filter · [ / ] switch Bee · l list skills · c skill check · v raw output · esc back"))
	return b.String()
}

func (m Model) viewSelfReview() string {
	var b strings.Builder
	bee := m.activeBee()
	beeLabel := "none"
	if bee != nil {
		beeLabel = fmt.Sprintf("%s (%s)", bee.DisplayName(), bee.Key())
	}
	b.WriteString(titleStyle.Render("BeeChief 🐝 — Self Review, Workshop & Swarm Review"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Selected Bee: %s (Safety: Workshop proposals are read-only and never auto-applied)\n\n", beeLabel)

	if m.activeProposal != nil {
		b.WriteString(headerStyle.Render("Workshop Proposal: " + m.activeProposal.Name))
		b.WriteString("\n")
		b.WriteString(m.activeProposal.Content)
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("esc close proposal detail"))
		return b.String()
	}

	if m.selfReview != nil {
		b.WriteString(headerStyle.Render("Latest Bee Self-Review (" + m.selfReview.AgentID + ")"))
		b.WriteString("\n")
		b.WriteString(truncateText(m.selfReview.Summary, 600))
		b.WriteString("\n\n")
	}

	b.WriteString(headerStyle.Render("Workshop Proposals"))
	b.WriteString("\n")
	if m.workshopList == nil || len(m.workshopList.Proposals) == 0 {
		b.WriteString("  (no Workshop proposals loaded — press 'w' to refresh or 's' to run self-review)\n")
	} else {
		for i, p := range m.workshopList.Proposals {
			line := fmt.Sprintf("%s — %s (uses=%d)", p.Name, p.Description, p.UseCount)
			if i == m.proposalCursor {
				b.WriteString(cursorStyle.Render("> " + line))
			} else {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}
	}

	if m.swarmSnap != nil {
		b.WriteString("\n")
		sum := m.swarmSnap.Summary
		b.WriteString(headerStyle.Render(fmt.Sprintf(
			"Swarm Review (%s) — total=%d complete=%d failed=%d skipped=%d proposals=%d",
			m.swarmSnap.Mode, sum.Total, sum.Completed, sum.Failed, sum.Skipped, sum.TotalProposals)))
		b.WriteString("\n")
		for _, it := range m.swarmSnap.Items {
			fmt.Fprintf(&b, "  [%s] %s: proposals=%d", it.State, it.Bee.Key(), it.ProposalCount)
			if it.Err != "" {
				fmt.Fprintf(&b, " err=%s", it.Err)
			} else if it.Output != "" {
				fmt.Fprintf(&b, " — %s", firstLine(it.Output))
			}
			b.WriteString("\n")
		}
	}

	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("s self-review Bee · w Workshop list · enter inspect proposal · 1/2/3 Swarm Review (Bee/Hive/All) · x cancel Swarm · [ / ] switch Bee · esc back"))
	return b.String()
}

func (m Model) viewTroubleshooting() string {
	var b strings.Builder
	hive := firstOr(m.activeHive(), "none")
	b.WriteString(titleStyle.Render("BeeChief 🐝 — Hive Health & Honey Report"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Selected Hive: %s · Sort by severity: %v\n\n", hive, m.sortSeverity)

	if m.healthReport == nil {
		b.WriteString("No Hive Health report yet. Press 'h' to run quick diagnostics or 'e' to export a Honey Report.\n")
	} else {
		pass, warn, fail, unavail := m.healthReport.Counts()
		fmt.Fprintf(&b, "Summary: %s · %s · %s · %d unavailable\n\n",
			okStyle.Render(fmt.Sprintf("%d pass", pass)),
			warnStyle.Render(fmt.Sprintf("%d warning", warn)),
			failStyle.Render(fmt.Sprintf("%d fail", fail)),
			unavail,
		)
		checks := m.healthReport.Checks
		if m.sortSeverity {
			checks = m.healthReport.SortedBySeverity()
		}
		for i, c := range checks {
			var badge string
			switch c.State {
			case diagnostics.StatePass:
				badge = okStyle.Render("[PASS]")
			case diagnostics.StateWarning:
				badge = warnStyle.Render("[WARN]")
			case diagnostics.StateFail:
				badge = failStyle.Render("[FAIL]")
			default:
				badge = helpStyle.Render("[UNAVAIL]")
			}
			line := fmt.Sprintf("%s %s — %s", badge, c.Name, c.Summary)
			if i == m.healthCursor {
				b.WriteString(cursorStyle.Render("> ") + line)
			} else {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}
		if m.healthCursor >= 0 && m.healthCursor < len(checks) {
			sel := checks[m.healthCursor]
			if sel.Detail != "" || len(sel.Remediation) > 0 {
				b.WriteString("\n")
				b.WriteString(headerStyle.Render("Check Details: " + sel.Name))
				b.WriteString("\n")
				if sel.Detail != "" {
					fmt.Fprintf(&b, "  Detail: %s\n", sel.Detail)
				}
				for _, rem := range sel.Remediation {
					fmt.Fprintf(&b, "  Remediation: • %s\n", rem)
				}
			}
		}
	}

	if m.exportPath != "" {
		b.WriteString("\n")
		b.WriteString(okStyle.Render("Honey Report exported: " + m.exportPath))
		b.WriteString("\n")
	}

	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ select check · [ / ] switch Hive · h run Hive Health · s sort severity · e export Honey Report · esc back"))
	return b.String()
}

func (m Model) viewServices() string {
	var b strings.Builder
	hive := firstOr(m.activeHive(), "none")
	tabs := []string{"systemd", "Docker", "Models & Ollama"}
	var tabHeader []string
	for i, t := range tabs {
		if i == m.servicesTab {
			tabHeader = append(tabHeader, cursorStyle.Render("["+t+"]"))
		} else {
			tabHeader = append(tabHeader, " "+t+" ")
		}
	}
	b.WriteString(titleStyle.Render("BeeChief 🐝 — Services & Model Troubleshooting"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Hive: %s · Subsystem: %s\n\n", hive, strings.Join(tabHeader, " · "))

	if m.pendingAction != "" {
		b.WriteString(warnStyle.Render(fmt.Sprintf(
			"CONFIRMATION REQUIRED: Run `systemctl %s %s` on Hive %s? [y/N]",
			m.pendingAction, m.pendingUnit, hive)))
		b.WriteString("\n\n")
	}

	if m.servicesReport == nil {
		b.WriteString("No service data loaded. Press 'r' or switch Hive with '[' / ']'.\n")
	} else {
		caps := m.servicesReport.Capabilities
		if caps.OS != "" || caps.OpenClaw.Installed {
			fmt.Fprintf(&b, "Capabilities: platform=%s/%s systemd=%v docker=%v(compose=%v) ollama=%v openclaw=%v\n\n",
				firstOr(caps.OS, "unknown"), firstOr(caps.Arch, "unknown"),
				caps.SystemdAvailable, caps.DockerAvailable, caps.DockerComposeAvailable,
				caps.OllamaAvailable, caps.OpenClaw.Installed)
		}
		switch m.servicesTab {
		case 0:
			sd := m.servicesReport.Systemd
			if !sd.Available {
				b.WriteString(warnStyle.Render("systemd unavailable on this Hive: " + firstOr(sd.Err, "not detected")))
				b.WriteString("\n")
			} else {
				fmt.Fprintf(&b, "systemd system state: %s\n\n", sd.SystemState)
				for i, u := range sd.Units {
					line := fmt.Sprintf("%s [%s/%s] — %s", u.Name, u.ActiveState, u.SubState, u.Description)
					if i == m.serviceCursor {
						b.WriteString(cursorStyle.Render("> " + line))
					} else {
						b.WriteString("  " + line)
					}
					b.WriteString("\n")
				}
				if m.selectedUnit != nil {
					b.WriteString("\n")
					b.WriteString(headerStyle.Render("Unit Inspection: " + m.selectedUnit.Name + " (enabled=" + firstOr(m.selectedUnit.EnabledState, "unknown") + ")"))
					b.WriteString("\n")
					if m.selectedUnit.Details != "" {
						b.WriteString(truncateText(m.selectedUnit.Details, 400) + "\n")
					}
					if m.selectedUnit.RecentLogs != "" {
						b.WriteString("Recent Logs:\n" + truncateText(m.selectedUnit.RecentLogs, 400) + "\n")
					}
				}
			}
		case 1:
			dk := m.servicesReport.Docker
			if !dk.Available {
				b.WriteString(warnStyle.Render("Docker unavailable on this Hive: " + firstOr(dk.Err, "not detected")))
				b.WriteString("\n")
			} else if len(dk.Containers) == 0 {
				fmt.Fprintf(&b, "Docker %s available (0 containers)\n", dk.Version)
			} else {
				fmt.Fprintf(&b, "Docker %s — %d container(s):\n\n", dk.Version, len(dk.Containers))
				for i, c := range dk.Containers {
					line := fmt.Sprintf("%s (%s) [%s: %s] ports=%s cpu=%s mem=%s",
						c.Name, c.Image, c.State, c.Status, firstOr(c.Ports, "-"), firstOr(c.CPU, "-"), firstOr(c.Mem, "-"))
					if i == m.serviceCursor {
						b.WriteString(cursorStyle.Render("> " + line))
					} else {
						b.WriteString("  " + line)
					}
					b.WriteString("\n")
				}
				if m.dockerLogText != "" {
					b.WriteString("\n")
					b.WriteString(headerStyle.Render("Container Recent Logs"))
					b.WriteString("\n")
					b.WriteString(truncateText(m.dockerLogText, 500) + "\n")
				}
			}
		case 2:
			md := m.servicesReport.Models
			fmt.Fprintf(&b, "Configured Default Model: %s\n", firstOr(md.ConfiguredDefault, "none"))
			fmt.Fprintf(&b, "Ollama Installed: %v · Ollama Reachable: %v\n", md.OllamaAvailable, md.OllamaReachable)
			if len(md.Mismatches) > 0 {
				b.WriteString("\n")
				b.WriteString(failStyle.Render("Detected Model / Provider Mismatches:"))
				b.WriteString("\n")
				for _, mm := range md.Mismatches {
					b.WriteString(failStyle.Render("  • " + mm))
					b.WriteString("\n")
				}
			}
			b.WriteString("\n")
			b.WriteString(headerStyle.Render("Local Ollama Models"))
			b.WriteString("\n")
			if len(md.OllamaModels) == 0 {
				b.WriteString("  (none)\n")
			}
			for _, om := range md.OllamaModels {
				fmt.Fprintf(&b, "  • %s (%s, %s)\n", om.Name, om.Size, om.Modified)
			}
		}
	}

	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("tab switch subsystem · [ / ] switch Hive · ↑/↓ select · enter inspect/logs · S/T/R start/stop/restart unit (confirm) · esc back"))
	return b.String()
}

func (m Model) viewLogs() string {
	var b strings.Builder
	hive := firstOr(m.activeHive(), "none")
	kind := logs.AllSources[m.logSourceIdx]
	cmdStr := logs.CommandString(kind, m.logTarget, logs.DefaultTail)

	b.WriteString(titleStyle.Render("BeeChief 🐝 — Unified Log Browser"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Hive: %s · Source: %s · Target: %s · Follow: %v\n",
		hive, kind, firstOr(m.logTarget, "(all)"), m.logFollowing)
	fmt.Fprintf(&b, "Filters: errorsOnly=%v warningsOnly=%v search=%q\n",
		m.logFilter.ErrorsOnly, m.logFilter.WarningsOnly, m.logFilter.Search)
	if m.showLogCmd {
		b.WriteString(headerStyle.Render("Command: " + cmdStr))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if m.logResult == nil {
		b.WriteString("No logs loaded. Press 't' to set unit/container/agent target or 'tab' to switch source.\n")
	} else {
		filtered := m.logFilter.Apply(m.logResult.Entries)
		if len(filtered) == 0 {
			b.WriteString("  (0 lines match current filter)\n")
		} else {
			maxLines := m.listViewportRows()
			start := m.logScroll
			if start >= len(filtered) {
				start = len(filtered) - 1
			}
			if start < 0 {
				start = 0
			}
			end := start + maxLines
			if end > len(filtered) {
				end = len(filtered)
			}
			for _, e := range filtered[start:end] {
				switch e.Level {
				case logs.LevelError:
					b.WriteString(m.renderFail("  " + e.Line))
				case logs.LevelWarning:
					b.WriteString(m.renderWarn("  " + e.Line))
				default:
					b.WriteString("  " + e.Line)
				}
				b.WriteString("\n")
			}
		}
	}

	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("tab source · [ / ] Hive · t target · e errors · w warnings · / search · f follow · c show cmd · esc back"))
	return b.String()
}

func (m Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("BeeChief 🐝 — Hive Configuration Wizard"))
	b.WriteString("\n")
	cfgPath := "hosts.yaml"
	if m.ops != nil && m.ops.ConfigPath() != "" {
		cfgPath = m.ops.ConfigPath()
	}
	fmt.Fprintf(&b, "Config file: %s (SSH keys/config reused; passwords are never stored)\n\n", cfgPath)

	if m.confirmDel != "" {
		b.WriteString(warnStyle.Render(fmt.Sprintf("CONFIRMATION REQUIRED: Remove Hive %q from configuration? [y/N]", m.confirmDel)))
		b.WriteString("\n\n")
	}

	if m.wizard.active {
		title := "Add Hive"
		if m.wizard.editing != "" {
			title = "Edit Hive: " + m.wizard.editing
		}
		b.WriteString(headerStyle.Render(title))
		b.WriteString("\n")
		for i, label := range wizardFieldLabels {
			val := m.wizard.fields[i]
			if val == "" {
				val = "<empty>"
			}
			line := fmt.Sprintf("%s: %s", label, val)
			if i == m.wizard.fieldIdx {
				b.WriteString(cursorStyle.Render("> " + line))
			} else {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}
		m.renderBannerAndInput(&b)
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("↑/↓ select field · enter edit value · t test connection & OpenClaw · s save & rescan · esc cancel"))
		return b.String()
	}

	b.WriteString(headerStyle.Render("Configured Hives"))
	b.WriteString("\n")
	if len(m.cfg.Hives) == 0 {
		b.WriteString("  (0 Hives configured — press 'l' to add local Hive or 'a' to add SSH Hive)\n")
	}
	for i, h := range m.cfg.Hives {
		desc := fmt.Sprintf("%s [%s]", h.Name, h.Kind())
		if h.Kind() == config.TypeSSH {
			desc += fmt.Sprintf(" host=%s", h.Host)
			if h.User != "" {
				desc += fmt.Sprintf(" user=%s", h.User)
			}
			if h.Port > 0 {
				desc += fmt.Sprintf(" port=%d", h.Port)
			}
		}
		if h.Label != "" {
			desc += fmt.Sprintf(" (%s)", h.Label)
		}
		if i == m.settingsIdx {
			b.WriteString(cursorStyle.Render("> " + desc))
		} else {
			b.WriteString("  " + desc)
		}
		b.WriteString("\n")
	}

	if m.lastHiveTest != nil {
		b.WriteString("\n")
		t := m.lastHiveTest
		b.WriteString(headerStyle.Render(fmt.Sprintf(
			"Last Connection Test (%s): reachable=%v openclaw=%v (%s)",
			t.Hive, t.Reachable, t.OpenClawPresent, firstOr(t.OpenClawVersion, t.Err))))
		b.WriteString("\n")
	}

	m.renderBannerAndInput(&b)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("a add SSH Hive · l add local Hive · e/enter edit · d delete (confirm) · t test Hive · r rescan · esc back"))
	return b.String()
}

func (m Model) renderBannerAndInput(b *strings.Builder) {
	if m.busy {
		b.WriteString("\n")
		b.WriteString(m.renderHelp("Working…"))
		b.WriteString("\n")
	}
	if m.statusBanner != "" {
		b.WriteString("\n")
		b.WriteString(m.renderWarn(m.statusBanner))
		b.WriteString("\n")
	}
	if m.input != inputNone {
		b.WriteString("\n")
		var prompt string
		switch m.input {
		case inputFilterBees:
			prompt = "Filter Bees (e.g. hive:qmu model:gpt): "
		case inputFilterSkills:
			prompt = "Filter Skills: "
		case inputInstruction:
			prompt = "Custom instruction: "
		case inputLogSearch:
			prompt = "Search logs: "
		case inputLogTarget:
			prompt = "Log target (unit/container/agent): "
		case inputWizardField:
			prompt = wizardFieldLabels[m.wizard.fieldIdx] + ": "
		}
		b.WriteString(m.renderCursor(prompt + m.inputBuffer + "█"))
		b.WriteString("\n")
	}
}

func truncateText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstOr(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
