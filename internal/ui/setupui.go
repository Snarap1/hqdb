package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/setup"
)

// setupStage is where the "hqdb setup" wizard is.
type setupStage int

const (
	setupPickSecrets setupStage = iota
	setupPickStore
	setupConfirm
	setupApplying
	setupDone
)

var (
	setupTitleStyle = lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	setupHintStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	setupRefStyle   = lipgloss.NewStyle().Foreground(colorLight)
	setupOkStyle    = lipgloss.NewStyle().Foreground(colorGreen)
	setupFailStyle  = lipgloss.NewStyle().Foreground(colorRed)
	setupSelStyle   = lipgloss.NewStyle().Foreground(colorAccent)
)

// SetupModel is the wizard over internal/setup: pick the literal
// credentials to move, pick the store, apply, see the results. The
// wizard never renders a secret, only where it lives.
type SetupModel struct {
	secrets  []setup.Secret
	stores   []setup.Store // the available ones
	runner   setup.Runner
	stage    setupStage
	cursor   int
	checked  map[int]bool
	storeIdx int
	results  []setup.Result
	err      error
	width    int
	height   int
}

// NewSetupModel builds the wizard over the found secrets and the
// stores it may offer. runner executes the stores' commands; tests
// inject a fake.
func NewSetupModel(secrets []setup.Secret, stores []setup.Store, runner setup.Runner) SetupModel {
	checked := make(map[int]bool, len(secrets))
	for i := range secrets {
		checked[i] = true
	}
	return SetupModel{
		secrets: secrets,
		stores:  setup.Available(stores),
		runner:  runner,
		checked: checked,
	}
}

// Init is a no-op; the wizard starts on its list.
func (m SetupModel) Init() tea.Cmd { return nil }

// Update moves the wizard through its stages.
func (m SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case setupResultsMsg:
		m.results, m.stage = msg.results, setupDone
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

// key handles one key for the current stage. q and ctrl+c always quit;
// esc steps back, enter steps forward.
func (m SetupModel) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.stage {
	case setupPickSecrets:
		switch s {
		case "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.secrets)-1 {
				m.cursor++
			}
		case " ", "enter":
			if s == " " {
				m.checked[m.cursor] = !m.checked[m.cursor]
			}
			if m.anyChecked() {
				m.stage, m.cursor = setupPickStore, 0
			}
		case "a":
			all := true
			for i := range m.secrets {
				all = all && m.checked[i]
			}
			for i := range m.secrets {
				m.checked[i] = !all
			}
		}
	case setupPickStore:
		switch s {
		case "q":
			return m, tea.Quit
		case "esc":
			m.stage, m.cursor = setupPickSecrets, 0
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.stores)-1 {
				m.cursor++
			}
		case "enter":
			m.stage = setupConfirm
		}
	case setupConfirm:
		switch s {
		case "q":
			return m, tea.Quit
		case "esc", "n":
			m.stage = setupPickStore
		case "enter", "y":
			m.stage = setupApplying
			return m, m.apply()
		}
	case setupDone:
		return m, tea.Quit
	}
	return m, nil
}

func (m SetupModel) anyChecked() bool {
	for i := range m.secrets {
		if m.checked[i] {
			return true
		}
	}
	return false
}

// apply moves every checked secret into the chosen store.
func (m SetupModel) apply() tea.Cmd {
	store := m.stores[m.storeIdx]
	var items []setup.Item
	for i, s := range m.secrets {
		if m.checked[i] {
			items = append(items, setup.Item{Secret: s, Store: store})
		}
	}
	return func() tea.Msg {
		return setupResultsMsg{results: setup.Apply(items, m.runner)}
	}
}

// setupResultsMsg carries the wizard's results back into the model.
type setupResultsMsg struct {
	results []setup.Result
}

// View renders the wizard's current stage.
func (m SetupModel) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if m.width == 0 || m.height == 0 {
		v.SetContent("")
		return v
	}
	v.SetContent(m.pad(m.body()))
	return v
}

// body renders the wizard's current stage.
func (m SetupModel) body() string {
	var b strings.Builder
	fmt.Fprintln(&b, setupTitleStyle.Render("hqdb setup"))
	fmt.Fprintln(&b)
	switch m.stage {
	case setupPickSecrets:
		fmt.Fprintln(&b, setupHintStyle.Render("Literal credentials found. Space toggles, a all, Enter continues, q quits."))
		fmt.Fprintln(&b)
		for i, s := range m.secrets {
			mark, row := " ", m.secretRow(s)
			if m.checked[i] {
				mark = "x"
			}
			style := setupSelStyle
			if i != m.cursor {
				style = lipgloss.NewStyle().Foreground(colorText)
			}
			fmt.Fprintf(&b, "%s %s\n", setupHintStyle.Render(mark), style.Render(row))
		}
	case setupPickStore:
		fmt.Fprintln(&b, setupHintStyle.Render("Move them into which store?"))
		fmt.Fprintln(&b)
		for i, s := range m.stores {
			style := setupSelStyle
			if i != m.cursor {
				style = lipgloss.NewStyle().Foreground(colorText)
			}
			fmt.Fprintf(&b, "  %s\n", style.Render(s.Title))
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, setupHintStyle.Render("The store is written through its own CLI; the config gets a reference that reads it back (ADR-0008)."))
	case setupConfirm:
		fmt.Fprintln(&b, setupHintStyle.Render("The config lines change to:"))
		fmt.Fprintln(&b)
		store := m.stores[m.storeIdx]
		for i, s := range m.secrets {
			if !m.checked[i] {
				continue
			}
			fmt.Fprintf(&b, "  %s %s\n", m.secretRow(s), setupOkStyle.Render("->"))
			fmt.Fprintf(&b, "    %s\n", setupRefStyle.Render(store.Reference(s.Profile)))
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, setupHintStyle.Render("Enter applies, Esc picks another store, q quits."))
	case setupApplying:
		fmt.Fprintln(&b, setupHintStyle.Render("Moving the credentials..."))
	case setupDone:
		for _, r := range m.results {
			style, word := setupOkStyle, "ok"
			if !r.Ok {
				style, word = setupFailStyle, "FAIL"
			}
			fmt.Fprintf(&b, "%s %s  %s\n", style.Render(word), m.secretRow(setup.Secret{Profile: r.Profile, Kind: r.Kind, File: ""}), setupRefStyle.Render(r.Reference))
			if r.Err != nil {
				fmt.Fprintf(&b, "    %s\n", setupFailStyle.Render(r.Err.Error()))
			}
			for _, n := range r.Notes {
				fmt.Fprintf(&b, "    %s\n", setupHintStyle.Render(n))
			}
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, setupHintStyle.Render("Run hqdb check to confirm. Any key quits."))
	}
	return b.String()
}

// secretRow renders one credential's location, never its value.
func (m SetupModel) secretRow(s setup.Secret) string {
	return fmt.Sprintf("%-16s %-14s %s", s.Profile, s.Kind, filepath.Base(s.File))
}

// pad keeps the view tall enough for the alternate screen.
func (m SetupModel) pad(s string) string {
	lines := strings.Count(s, "\n") + 1
	h := m.height
	if h < lines+1 {
		return s
	}
	return s + strings.Repeat("\n", h-lines)
}

// RunSetup is "hqdb setup": it finds the literal credentials and, when
// there are some, runs the wizard that moves them into a store. With
// none found, it says so and exits quietly.
func RunSetup(cfg *config.Config) error {
	found, err := setup.Find(cfg)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Println("no literal credentials found; nothing to set up")
		return nil
	}
	envFile, err := setup.DefaultEnvFile()
	if err != nil {
		envFile = "" // the env store falls back to its own default
	}
	stores := setup.Stores(envFile)
	if len(setup.Available(stores)) == 0 {
		// No store here (Windows: hqdb offers none there, ADR-0009), so
		// there is nothing a wizard could move the secret into.
		fmt.Println("no secret store is available on this machine; reference the secret by hand instead")
		fmt.Println("see \"Moving credentials into a store\" in the config guide")
		return nil
	}
	m := NewSetupModel(found, stores, setup.ExecRunner())
	_, err = tea.NewProgram(m).Run()
	return err
}
