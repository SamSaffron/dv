package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dv/internal/ai"
	"dv/internal/ai/discourse"
	"dv/internal/ai/providers"
	"dv/internal/huh"
)

type aiFocus int

const (
	focusConfigured aiFocus = iota
	focusCatalog
)

type aiMode int

const (
	modeLoading aiMode = iota
	modeBrowse
	modeCreate
	modeConfirmDelete
	modeSaving
	modeTesting
)

type aiConfigOptions struct {
	state        ai.LLMState
	catalog      ai.ProviderCatalog
	client       *discourse.Client
	env          map[string]string
	container    string
	discourseDir string
	ctx          context.Context
	loadingState bool
	cacheDir     string
}

type aiConfigModel struct {
	ctx             context.Context
	client          *discourse.Client
	container       string
	workdir         string
	env             map[string]string
	cacheDir        string
	focus           aiFocus
	mode            aiMode
	state           ai.LLMState
	catalog         ai.ProviderCatalog
	width           int
	height          int
	status          string
	toast           string
	errMsg          string
	busy            bool
	busyMessage     string
	loadingProgress []string
	savingMessage   string
	testingMessage  string
	testResult      string
	testError       string
	help            help.Model
	llmList         list.Model
	modelList       list.Model
	spinner         spinner.Model
	form            *createForm
	deleteLLM       *ai.LLMModel
}

func newAiConfigModel(opts aiConfigOptions) aiConfigModel {
	var llmList, modelList list.Model
	mode := modeBrowse
	loadingProgress := []string{}

	if opts.loadingState {
		mode = modeLoading
		loadingProgress = []string{"Starting up..."}
	} else {
		llmItems := make([]list.Item, 0, len(opts.state.Models))
		for _, m := range opts.state.Models {
			llmItems = append(llmItems, llmItem{model: m, isDefault: m.ID == opts.state.DefaultID})
		}
		llmDelegate := list.NewDefaultDelegate()
		llmList = list.New(llmItems, llmDelegate, 0, 0)
		llmList.Title = "Configured Models"
		llmList.SetShowStatusBar(false)
		llmList.SetFilteringEnabled(true)
		llmList.SetShowPagination(false)

		providerItems := catalogItems(opts.catalog)
		providerDelegate := list.NewDefaultDelegate()
		modelList = list.New(providerItems, providerDelegate, 0, 0)
		modelList.Title = "Provider Catalog"
		modelList.SetShowStatusBar(false)
		modelList.SetShowPagination(false)
	}

	sp := spinner.New()
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	return aiConfigModel{
		ctx:             opts.ctx,
		client:          opts.client,
		container:       opts.container,
		workdir:         opts.discourseDir,
		env:             opts.env,
		cacheDir:        opts.cacheDir,
		state:           opts.state,
		catalog:         opts.catalog,
		mode:            mode,
		loadingProgress: loadingProgress,
		help:            help.New(),
		llmList:         llmList,
		modelList:       modelList,
		spinner:         sp,
	}
}

func catalogItems(cat ai.ProviderCatalog) []list.Item {
	var items []list.Item
	for _, entry := range cat.Entries {
		for _, model := range entry.Models {
			items = append(items, providerItem{entryID: entry.ID, model: model})
		}
		if len(entry.Models) == 0 {
			items = append(items, providerItem{entryID: entry.ID, locked: !entry.HasCredentials, errText: entry.Error})
		}
	}
	return items
}

func (m aiConfigModel) Init() tea.Cmd {
	if m.mode == modeLoading {
		return tea.Batch(
			m.spinner.Tick,
			m.initLoadCmd(),
		)
	}
	return m.spinner.Tick
}

type aiStateMsg struct {
	state  ai.LLMState
	notice string
}

type aiErrMsg struct {
	err error
}

type aiTestMsg struct {
	err error
}

type aiLoadingMsg struct {
	step string
}

type aiInitCompleteMsg struct {
	state   ai.LLMState
	catalog ai.ProviderCatalog
	err     error
}

func (m aiConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.KeyMsg:
		if m.mode == modeCreate && m.form != nil {
			return m.updateForm(msg)
		}
		if m.mode == modeConfirmDelete && m.deleteLLM != nil {
			return m.updateDeleteConfirm(msg)
		}
		if m.mode == modeTesting {
			return m.updateTestingModal(msg)
		}

		// Check if we're currently filtering - if so, don't process single-key shortcuts
		isFiltering := false
		if m.focus == focusConfigured {
			isFiltering = m.llmList.FilterState() == list.Filtering
		} else if m.focus == focusCatalog {
			isFiltering = m.modelList.FilterState() == list.Filtering
		}

		// Only process single-key shortcuts when not filtering
		if !isFiltering {
			switch msg.String() {
			case "left", "h":
				// Move to left pane (configured models)
				m.focus = focusConfigured
				return m, nil
			case "right", "l":
				// Move to right pane (catalog)
				m.focus = focusCatalog
				return m, nil
			case "tab", "ctrl+i":
				// Tab also works to cycle forward
				if m.focus == focusConfigured {
					m.focus = focusCatalog
				} else {
					m.focus = focusConfigured
				}
				return m, nil
			case "shift+tab":
				// Shift+Tab cycles backward
				if m.focus == focusCatalog {
					m.focus = focusConfigured
				} else {
					m.focus = focusCatalog
				}
				return m, nil
			case "q", "esc", "ctrl+c":
				return m, tea.Quit
			case "r":
				m.busy = true
				m.busyMessage = "Refreshing models..."
				return m, m.fetchStateCmd("Refreshed models")
			case "enter":
				if m.focus == focusConfigured {
					if item, ok := m.llmList.SelectedItem().(llmItem); ok {
						if item.model.ID != m.state.DefaultID {
							m.busy = true
							m.busyMessage = "Setting default model..."
							return m, m.setDefaultCmd(item.model.ID, item.model.DisplayName)
						}
					}
				} else if m.focus == focusCatalog {
					if item, ok := m.modelList.SelectedItem().(providerItem); ok && item.model.ID != "" && !item.locked {
						m.form = newCreateForm(item.entryID, item.model, m.state.Meta, m.env)
						m.mode = modeCreate
						m.toast = ""
						m.errMsg = ""
						return m, nil
					}
				}
			case "e":
				if m.focus == focusConfigured {
					if item, ok := m.llmList.SelectedItem().(llmItem); ok {
						m.form = newEditForm(item.model, m.state.Meta, item.isDefault)
						m.mode = modeCreate
						m.toast = ""
						m.errMsg = ""
						return m, nil
					}
				}
			case "d", "delete":
				if m.focus == focusConfigured {
					if item, ok := m.llmList.SelectedItem().(llmItem); ok {
						target := item.model
						m.deleteLLM = &target
						m.mode = modeConfirmDelete
						m.errMsg = ""
						m.toast = ""
						return m, nil
					}
				}
			}
		} else {
			// When filtering, only allow these specific keys
			switch msg.String() {
			case "esc", "ctrl+c":
				// Allow exit even when filtering
				return m, tea.Quit
			}
		}
	case aiStateMsg:
		m.busy = false
		m.busyMessage = ""
		m.savingMessage = ""
		m.mode = modeBrowse
		m.form = nil
		m.deleteLLM = nil
		m.state = msg.state
		m.toast = msg.notice
		m.errMsg = ""
		m.updateLists()
	case aiErrMsg:
		m.busy = false
		m.busyMessage = ""
		m.savingMessage = ""
		if m.mode == modeSaving {
			m.mode = modeBrowse
		}
		m.errMsg = msg.err.Error()
	case aiTestMsg:
		if msg.err != nil {
			m.testError = msg.err.Error()
			m.testResult = "failed"
		} else {
			m.testResult = "success"
			m.testError = ""
		}
	case aiLoadingMsg:
		m.loadingProgress = append(m.loadingProgress, msg.step)
	case aiInitCompleteMsg:
		if msg.err != nil {
			m.mode = modeBrowse
			m.errMsg = fmt.Sprintf("Initialization error: %v", msg.err)
			return m, nil
		}
		m.state = msg.state
		m.catalog = msg.catalog
		m.mode = modeBrowse

		llmItems := make([]list.Item, 0, len(m.state.Models))
		for _, model := range m.state.Models {
			llmItems = append(llmItems, llmItem{model: model, isDefault: model.ID == m.state.DefaultID})
		}
		llmDelegate := list.NewDefaultDelegate()
		m.llmList = list.New(llmItems, llmDelegate, 0, 0)
		m.llmList.Title = "Configured Models"
		m.llmList.SetShowStatusBar(false)
		m.llmList.SetFilteringEnabled(true)
		m.llmList.SetShowPagination(false)

		providerItems := catalogItems(m.catalog)
		providerDelegate := list.NewDefaultDelegate()
		m.modelList = list.New(providerItems, providerDelegate, 0, 0)
		m.modelList.Title = "Provider Catalog"
		m.modelList.SetShowStatusBar(false)
		m.modelList.SetShowPagination(false)

		m.resize()
		m.toast = "Ready!"
	}

	var cmds []tea.Cmd
	// Only update lists if they're initialized (not in loading/saving/testing mode)
	if m.mode != modeLoading && m.mode != modeSaving && m.mode != modeTesting {
		if m.focus == focusConfigured {
			listModel, cmd := m.llmList.Update(msg)
			m.llmList = listModel
			cmds = append(cmds, cmd)
		} else if m.mode == modeBrowse {
			modelList, cmd := m.modelList.Update(msg)
			m.modelList = modelList
			cmds = append(cmds, cmd)
		}
	}
	if m.busy || m.mode == modeLoading || m.mode == modeSaving || m.mode == modeTesting {
		sp, cmd := m.spinner.Update(msg)
		m.spinner = sp
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m aiConfigModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		m.mode = modeBrowse
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mode = modeBrowse
		m.form = nil
		return m, nil
	case "tab", "down":
		m.form.advance()
		return m, nil
	case "right":
		m.form.advance()
		return m, nil
	case "shift+tab", "up":
		m.form.retreat()
		return m, nil
	case "left":
		m.form.retreat()
		return m, nil
	case "ctrl+s", "enter":
		payload, err := m.form.payload()
		if err != nil {
			m.form.err = err.Error()
			return m, nil
		}
		m.mode = modeSaving
		m.form = nil
		if payload.ExistingID > 0 {
			m.savingMessage = fmt.Sprintf("Updating %s...", payload.DisplayName)
			return m, m.updateModelCmd(payload.ExistingID, payload)
		}
		m.savingMessage = fmt.Sprintf("Creating %s...", payload.DisplayName)
		return m, m.createModelCmd(payload)
	case "ctrl+t":
		payload, err := m.form.payload()
		if err != nil {
			m.form.err = err.Error()
			return m, nil
		}
		if strings.TrimSpace(payload.APIKey) == "" && !m.form.isEdit() {
			m.form.err = "Enter an API key to run a test"
			return m, nil
		}
		// Switch to testing mode IMMEDIATELY - show modal right away
		m.mode = modeTesting
		m.testingMessage = fmt.Sprintf("Testing connection to %s...", payload.DisplayName)
		m.testResult = ""
		m.testError = ""
		// Return both spinner tick and test command together
		return m, tea.Batch(m.spinner.Tick, m.testModelCmd(payload))
	case " ":
		if field := m.form.currentField(); field != nil {
			if field.Kind == fieldBool {
				field.BoolValue = !field.BoolValue
				return m, nil
			} else if field.Kind == fieldSelect {
				// Cycle through select options
				if len(field.SelectValues) > 0 {
					currentIdx := 0
					for i, opt := range field.SelectValues {
						if opt == field.SelectValue {
							currentIdx = i
							break
						}
					}
					nextIdx := (currentIdx + 1) % len(field.SelectValues)
					field.SelectValue = field.SelectValues[nextIdx]
				}
				return m, nil
			}
		}
	}
	field := m.form.currentField()
	if field == nil || field.Kind != fieldInput {
		return m, nil
	}
	var cmd tea.Cmd
	field.Model, cmd = field.Model.Update(msg)
	return m, cmd
}

func (m aiConfigModel) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.deleteLLM == nil {
		m.mode = modeBrowse
		return m, nil
	}
	switch msg.String() {
	case "y", "enter":
		target := m.deleteLLM
		m.mode = modeBrowse
		m.deleteLLM = nil
		m.busy = true
		m.busyMessage = "Deleting model..."
		return m, m.deleteModelCmd(target.ID, target.DisplayName)
	case "n", "esc":
		m.mode = modeBrowse
		m.deleteLLM = nil
		return m, nil
	}
	return m, nil
}

func (m *aiConfigModel) resize() {
	if m.width == 0 || m.height == 0 {
		return
	}
	// Skip resize if we're in loading mode (lists not initialized yet)
	if m.mode == modeLoading {
		return
	}
	bodyHeight := max(10, m.height-8)
	detailHeight := 7
	listHeight := bodyHeight - detailHeight
	leftWidth := max(30, m.width/2-2)
	rightWidth := max(30, m.width-leftWidth-4)
	m.llmList.SetSize(leftWidth, listHeight)
	m.modelList.SetSize(rightWidth, listHeight)
}

func (m *aiConfigModel) updateLists() {
	items := make([]list.Item, 0, len(m.state.Models))
	for _, entry := range m.state.Models {
		items = append(items, llmItem{model: entry, isDefault: entry.ID == m.state.DefaultID})
	}
	m.llmList.SetItems(items)
	m.catalog = m.catalog // no-op placeholder; kept for future use.
}

func (m aiConfigModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}
	left := m.llmList.View()
	right := m.modelList.View()
	if m.focus == focusConfigured {
		left = lipgloss.NewStyle().Border(lipgloss.ThickBorder(), true).BorderForeground(lipgloss.Color("212")).Render(left)
		right = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).Render(right)
	} else {
		right = lipgloss.NewStyle().Border(lipgloss.ThickBorder(), true).BorderForeground(lipgloss.Color("212")).Render(right)
		left = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).Render(left)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, lipgloss.NewStyle().Width(2).Render(""), right)

	status := m.renderStatusLine()
	detail := m.renderDetail()
	toast := ""
	if m.toast != "" {
		toast = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(m.toast)
	}
	errLine := ""
	if m.errMsg != "" {
		errLine = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(m.errMsg)
	}
	busy := ""
	if m.busy {
		busyMsg := "Working..."
		if m.busyMessage != "" {
			busyMsg = m.busyMessage
		}
		busy = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Render(m.spinner.View() + " " + busyMsg)
	}

	view := fmt.Sprintf("%s\n%s\n\n%s\n%s\n%s", status, body, detail, toast, errLine)
	if busy != "" {
		view = busy + "\n" + view
	}

	switch m.mode {
	case modeLoading:
		return m.renderLoadingScreen()
	case modeSaving:
		return m.renderSavingModal()
	case modeTesting:
		return m.renderTestingModal()
	case modeCreate:
		if m.form != nil {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.form.View(), lipgloss.WithWhitespaceChars("░"), lipgloss.WithWhitespaceForeground(lipgloss.Color("8")))
		}
	case modeConfirmDelete:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderDeleteModal(), lipgloss.WithWhitespaceChars("░"), lipgloss.WithWhitespaceForeground(lipgloss.Color("8")))
	}
	return view
}

func (m aiConfigModel) renderStatusLine() string {
	defaultName := "None"
	for _, model := range m.state.Models {
		if model.ID == m.state.DefaultID {
			defaultName = model.DisplayName
			break
		}
	}
	keyParts := []string{}
	for _, entry := range []struct {
		Label string
		Keys  []string
	}{
		{"OpenAI", []string{"OPENAI_API_KEY"}},
		{"Anthropic", []string{"ANTHROPIC_API_KEY"}},
		{"OpenRouter", []string{"OPENROUTER_API_KEY", "OPENROUTER_KEY"}},
		{"Groq", []string{"GROQ_API_KEY"}},
		{"Gemini", []string{"GEMINI_API_KEY"}},
	} {
		val := firstNonEmpty(m.env, entry.Keys...)
		if val != "" {
			keyParts = append(keyParts, fmt.Sprintf("%s ✓", entry.Label))
		} else {
			keyParts = append(keyParts, fmt.Sprintf("%s ·", entry.Label))
		}
	}
	role := fmt.Sprintf("Container: %s  Default: %s", m.container, defaultName)
	return fmt.Sprintf("%s\nKeys: %s", role, strings.Join(keyParts, "  "))
}

func (m aiConfigModel) renderDetail() string {
	item, ok := m.llmList.SelectedItem().(llmItem)
	if !ok {
		return "Select a model to see details."
	}
	llm := item.model
	lines := []string{
		fmt.Sprintf("%s (%s)", llm.DisplayName, llm.Name),
		fmt.Sprintf("Provider: %s  Tokenizer: %s", llm.Provider, shortTokenizer(llm.Tokenizer)),
		fmt.Sprintf("Prompt tokens: %d  Output tokens: %d", llm.MaxPromptTokens, llm.MaxOutputTokens),
		fmt.Sprintf("Pricing ($/1M): input %.4f cached %.4f output %.4f", llm.InputCost, llm.CachedInputCost, llm.OutputCost),
		fmt.Sprintf("Endpoint: %s", llm.URL),
		fmt.Sprintf("Used by: %s", joinUsage(llm.UsedBy)),
	}
	return lipgloss.NewStyle().Width(m.width).Render(strings.Join(lines, "\n"))
}

func (m aiConfigModel) fetchStateCmd(notice string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		state, err := client.FetchState(ctx)
		if err != nil {
			return aiErrMsg{err}
		}
		return aiStateMsg{state: state, notice: notice}
	}
}

func (m aiConfigModel) setDefaultCmd(id int64, name string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if err := client.SetDefaultLLM(ctx, id); err != nil {
			return aiErrMsg{err}
		}
		state, err := client.FetchState(ctx)
		if err != nil {
			return aiErrMsg{err}
		}
		return aiStateMsg{state: state, notice: fmt.Sprintf("Set %s as default", name)}
	}
}

func (m aiConfigModel) createModelCmd(payload discourse.CreateModelInput) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if _, err := client.CreateModel(ctx, payload); err != nil {
			return aiErrMsg{err}
		}
		state, err := client.FetchState(ctx)
		if err != nil {
			return aiErrMsg{err}
		}
		return aiStateMsg{state: state, notice: fmt.Sprintf("Added %s", payload.DisplayName)}
	}
}

func (m aiConfigModel) testModelCmd(payload discourse.CreateModelInput) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if err := client.TestModel(ctx, payload); err != nil {
			return aiTestMsg{err: err}
		}
		return aiTestMsg{}
	}
}

func (m aiConfigModel) updateModelCmd(id int64, payload discourse.CreateModelInput) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if err := client.UpdateModel(ctx, id, payload); err != nil {
			return aiErrMsg{err}
		}
		state, err := client.FetchState(ctx)
		if err != nil {
			return aiErrMsg{err}
		}
		return aiStateMsg{state: state, notice: fmt.Sprintf("Updated %s", payload.DisplayName)}
	}
}

func (m aiConfigModel) deleteModelCmd(id int64, name string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if err := client.DeleteModel(ctx, id); err != nil {
			return aiErrMsg{err}
		}
		state, err := client.FetchState(ctx)
		if err != nil {
			return aiErrMsg{err}
		}
		return aiStateMsg{state: state, notice: fmt.Sprintf("Deleted %s", name)}
	}
}

func (m aiConfigModel) renderDeleteModal() string {
	name := "this model"
	if m.deleteLLM != nil {
		name = m.deleteLLM.DisplayName
	}
	content := fmt.Sprintf("Delete %s?\n\nThis removes the LLM from Discourse. Features using it will stop until reassigned.\n\nPress Y to delete or N to cancel.", name)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("203")).
		Padding(1, 2).
		Width(80).
		Render(content)
}

func (m aiConfigModel) renderSavingModal() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("212")).
		Padding(0, 0, 1, 0)

	spinnerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("212"))

	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("246")).
		Padding(1, 0)

	message := "Saving configuration..."
	if m.savingMessage != "" {
		message = m.savingMessage
	}

	lines := []string{
		titleStyle.Render("💾 Saving Configuration"),
		"",
		spinnerStyle.Render(m.spinner.View() + " " + message),
		"",
		messageStyle.Render("This may take a moment..."),
	}

	content := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("212")).
		Padding(2, 4).
		Width(60)

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		box.Render(content),
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("8")),
	)
}

func (m aiConfigModel) renderTestingModal() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("212")).
		Padding(0, 0, 1, 0)

	spinnerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("212"))

	successStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42")).
		Bold(true).
		Padding(0, 2)

	errorTitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("203")).
		Bold(true)

	errorMsgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("203")).
		Background(lipgloss.Color("52")).
		Padding(1, 2).
		Width(60)

	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("246"))

	lines := []string{
		titleStyle.Render("🧪 Testing LLM Connection"),
		"",
	}

	if m.testResult == "" {
		// Still testing
		message := "Testing connection..."
		if m.testingMessage != "" {
			message = m.testingMessage
		}
		lines = append(lines,
			spinnerStyle.Render(m.spinner.View()+" "+message),
			"",
			messageStyle.Render("Sending test request to API..."),
			"",
			messageStyle.Render("This may take a few seconds..."),
		)
	} else if m.testResult == "success" {
		// Success!
		lines = append(lines,
			"",
			successStyle.Render("✅  TEST PASSED  ✅"),
			"",
			"",
			messageStyle.Render("Connection verified successfully."),
			messageStyle.Render("The API responded correctly."),
			"",
			"",
			messageStyle.Render("Press Enter to continue"),
		)
	} else {
		// Failed - make error VERY visible
		lines = append(lines,
			"",
			errorTitleStyle.Render("❌  TEST FAILED  ❌"),
			"",
			"",
			errorMsgStyle.Render(m.testError),
			"",
			"",
			messageStyle.Render("Press Enter to return to form"),
		)
	}

	content := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("212")).
		Padding(2, 4).
		Width(70)

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		box.Render(content),
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("8")),
	)
}

func (m aiConfigModel) updateTestingModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Only allow returning to form once test is complete
	if m.testResult != "" {
		switch msg.String() {
		case "enter", "esc":
			// Return to form
			m.mode = modeCreate
			if m.testResult == "success" && m.form != nil {
				m.form.testSuccess = true
				m.form.err = ""
			}
			return m, nil
		}
	}
	return m, nil
}

type llmItem struct {
	model     ai.LLMModel
	isDefault bool
}

func (i llmItem) Title() string {
	title := i.model.DisplayName
	if i.isDefault {
		title = "★ " + title
	}
	return title
}

func (i llmItem) Description() string {
	return fmt.Sprintf("%s · %d tokens · $%.2f/$%.2f", i.model.Provider, i.model.MaxPromptTokens, i.model.InputCost, i.model.OutputCost)
}

func (i llmItem) FilterValue() string {
	return i.model.DisplayName
}

type providerItem struct {
	entryID string
	model   ai.ProviderModel
	locked  bool
	errText string
}

func (i providerItem) Title() string {
	if i.locked {
		return fmt.Sprintf("%s (add API key to unlock)", strings.Title(i.entryID))
	}
	return fmt.Sprintf("%s", i.model.DisplayName)
}

func (i providerItem) Description() string {
	if i.locked {
		if i.errText != "" {
			return i.errText
		}
		return "No credentials detected"
	}
	// Handle free models
	if i.model.InputCost == 0 && i.model.OutputCost == 0 {
		return fmt.Sprintf("%s · ctx %d · FREE", i.model.Provider, i.model.ContextTokens)
	}
	return fmt.Sprintf("%s · ctx %d · $%.4f/$%.4f", i.model.Provider, i.model.ContextTokens, i.model.InputCost, i.model.OutputCost)
}

func (i providerItem) FilterValue() string {
	if i.model.ID != "" {
		return i.model.ID
	}
	return i.entryID
}

type fieldKind int

const (
	fieldInput fieldKind = iota
	fieldBool
	fieldSelect
)

type formMode int

const (
	formModeCreate formMode = iota
	formModeEdit
)

type formField struct {
	Key          string
	Label        string
	Kind         fieldKind
	Model        textinput.Model
	BoolValue    bool
	SelectValue  string
	SelectValues []string
	IsProvider   bool
}

type createForm struct {
	entryID     string
	fields      []*formField
	focusIndex  int
	err         string
	mode        formMode
	editingID   int64
	testSuccess bool
}

func newCreateForm(entryID string, model ai.ProviderModel, meta ai.LLMMetadata, env map[string]string) *createForm {
	fields := []*formField{
		newTextField("display_name", "Display Name", model.DisplayName, false),
		newTextField("name", "Short Name", model.ID, false),
		newTextField("provider", "Provider", providerSlug(entryID), false),
		newTextField("tokenizer", "Tokenizer", defaultTokenizerFor(model.Provider, meta), false),
		newTextField("url", "API URL", model.Endpoint, false),
		newTextField("api_key", "API Key", firstNonEmpty(env, providerKeyHints(entryID)...), true),
		newTextField("max_prompt_tokens", "Max Prompt Tokens", safeInt(model.ContextTokens, 131072), false),
		newTextField("max_output_tokens", "Max Output Tokens", safeInt(model.ContextTokens/4, 4096), false),
		newTextField("input_cost", "Input Cost ($/1M)", fmt.Sprintf("%.4f", model.InputCost), false),
		newTextField("cached_input_cost", "Cached Input Cost ($/1M)", fmt.Sprintf("%.4f", model.CachedInputCost), false),
		newTextField("output_cost", "Output Cost ($/1M)", fmt.Sprintf("%.4f", model.OutputCost), false),
		newBoolField("set_default", "Set as default", true),
		newBoolField("enabled_chat_bot", "Enable chat bot", true),
		newBoolField("vision_enabled", "Enable vision", true),
	}
	providerKey := providerSlug(entryID)
	defaults := map[string]interface{}{}
	if providerKey == "open_ai" {
		defaults["enable_responses_api"] = true
	}
	fields = append(fields, buildProviderParamFields(providerKey, meta, nil, defaults)...)
	f := &createForm{
		entryID: entryID,
		fields:  fields,
		mode:    formModeCreate,
	}
	f.updateFocus()
	return f
}

func newEditForm(llm ai.LLMModel, meta ai.LLMMetadata, isDefault bool) *createForm {
	fields := []*formField{
		newTextField("display_name", "Display Name", llm.DisplayName, false),
		newTextField("name", "Short Name", llm.Name, false),
		newTextField("provider", "Provider", llm.Provider, false),
		newTextField("tokenizer", "Tokenizer", llm.Tokenizer, false),
		newTextField("url", "API URL", llm.URL, false),
		newTextField("api_key", "API Key", "", true),
		newTextField("max_prompt_tokens", "Max Prompt Tokens", fmt.Sprintf("%d", llm.MaxPromptTokens), false),
		newTextField("max_output_tokens", "Max Output Tokens", fmt.Sprintf("%d", llm.MaxOutputTokens), false),
		newTextField("input_cost", "Input Cost ($/1M)", fmt.Sprintf("%.4f", llm.InputCost), false),
		newTextField("cached_input_cost", "Cached Input Cost ($/1M)", fmt.Sprintf("%.4f", llm.CachedInputCost), false),
		newTextField("output_cost", "Output Cost ($/1M)", fmt.Sprintf("%.4f", llm.OutputCost), false),
		newBoolField("set_default", "Set as default", isDefault),
		newBoolField("enabled_chat_bot", "Enable chat bot", llm.EnabledChatBot),
		newBoolField("vision_enabled", "Enable vision", llm.VisionEnabled),
	}
	fields = append(fields, buildProviderParamFields(llm.Provider, meta, llm.ProviderParams, nil)...)
	f := &createForm{
		fields:    fields,
		entryID:   providerSlug(llm.Provider),
		mode:      formModeEdit,
		editingID: llm.ID,
	}
	fields[5].Model.Placeholder = "Leave blank to keep current key"
	f.updateFocus()
	return f
}

func newTextField(key, label, value string, mask bool) *formField {
	var ti textinput.Model
	if mask {
		builder := huh.NewInput().
			Title("Enter your Credentials").
			Prompt(label).
			Password(true)
		if value != "" {
			builder.Value(&value)
		}
		ti = builder.Model()
	} else {
		ti = textinput.New()
		ti.Placeholder = label
		ti.SetValue(value)
		ti.CharLimit = 200
	}
	return &formField{Key: key, Label: label, Kind: fieldInput, Model: ti}
}

func newBoolField(key, label string, value bool) *formField {
	return &formField{Key: key, Label: label, Kind: fieldBool, BoolValue: value}
}

func newSelectField(key, label, value string, options []string) *formField {
	// Ensure value is in options, otherwise use first option
	if value == "" && len(options) > 0 {
		value = options[0]
	}
	found := false
	for _, opt := range options {
		if opt == value {
			found = true
			break
		}
	}
	if !found && len(options) > 0 {
		value = options[0]
	}
	return &formField{
		Key:          key,
		Label:        label,
		Kind:         fieldSelect,
		SelectValue:  value,
		SelectValues: options,
	}
}

func safeInt(val int, fallback int) string {
	if val <= 0 {
		val = fallback
	}
	return fmt.Sprintf("%d", val)
}

func (f *createForm) advance() {
	f.focusIndex = (f.focusIndex + 1) % len(f.fields)
	f.updateFocus()
}

func (f *createForm) retreat() {
	f.focusIndex--
	if f.focusIndex < 0 {
		f.focusIndex = len(f.fields) - 1
	}
	f.updateFocus()
}

func (f *createForm) updateFocus() {
	for i, field := range f.fields {
		if field.Kind != fieldInput {
			continue
		}
		if i == f.focusIndex {
			field.Model.Focus()
		} else {
			field.Model.Blur()
		}
	}
}

func (f *createForm) currentField() *formField {
	if len(f.fields) == 0 {
		return nil
	}
	return f.fields[f.focusIndex]
}

func (f *createForm) View() string {
	title := "Configure New LLM"
	if f.mode == formModeEdit {
		title = fmt.Sprintf("Edit %s", f.value("display_name"))
	}
	lines := []string{title, ""}
	for i, field := range f.fields {
		var rendered string
		switch field.Kind {
		case fieldInput:
			rendered = fmt.Sprintf("%s:\n%s", field.Label, field.Model.View())
		case fieldBool:
			box := "[ ]"
			if field.BoolValue {
				box = "[x]"
			}
			rendered = fmt.Sprintf("%s %s", box, field.Label)
		case fieldSelect:
			opts := strings.Join(field.SelectValues, " | ")
			selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
			rendered = fmt.Sprintf("%s: %s\nOptions: %s", field.Label, selectedStyle.Render(field.SelectValue), opts)
		}
		if i == f.focusIndex {
			rendered = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Render(rendered)
		}
		lines = append(lines, rendered, "")
	}
	lines = append(lines,
		"[Space] toggle/cycle · Tab to move · Shift+Tab to go back",
		"Enter to save · Esc to cancel · Ctrl+T to test",
	)
	if f.testSuccess {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("✅ Test passed! You can now save the configuration."))
	}
	if f.err != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(f.err))
	}
	box := lipgloss.NewStyle().Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("212")).Width(80)
	return box.Render(strings.Join(lines, "\n"))
}

func (f *createForm) payload() (discourse.CreateModelInput, error) {
	var payload discourse.CreateModelInput
	if f.isEdit() {
		payload.ExistingID = f.targetID()
	}
	payload.Provider = providerSlug(f.value("provider"))
	payload.DisplayName = strings.TrimSpace(f.value("display_name"))
	payload.Name = strings.TrimSpace(f.value("name"))
	payload.Tokenizer = strings.TrimSpace(f.value("tokenizer"))
	payload.URL = strings.TrimSpace(f.value("url"))
	apiKey := strings.TrimSpace(f.value("api_key"))
	if payload.Provider == "" {
		if slug := providerSlug(f.entryID); slug != "" {
			payload.Provider = slug
		}
	}
	if payload.Provider == "" || payload.DisplayName == "" || payload.Name == "" || payload.Tokenizer == "" || payload.URL == "" {
		return payload, fmt.Errorf("all fields are required")
	}
	if apiKey == "" {
		if !f.isEdit() {
			return payload, fmt.Errorf("API key is required")
		}
	} else {
		payload.APIKey = apiKey
	}
	promptTokens, err := strconv.Atoi(f.value("max_prompt_tokens"))
	if err != nil {
		return payload, fmt.Errorf("max prompt tokens must be a number")
	}
	outputTokens, err := strconv.Atoi(f.value("max_output_tokens"))
	if err != nil {
		return payload, fmt.Errorf("max output tokens must be a number")
	}
	payload.MaxPromptTokens = promptTokens
	payload.MaxOutputTokens = outputTokens
	payload.InputCost, err = strconv.ParseFloat(f.value("input_cost"), 64)
	if err != nil {
		return payload, fmt.Errorf("input cost must be numeric")
	}
	payload.CachedInputCost, err = strconv.ParseFloat(f.value("cached_input_cost"), 64)
	if err != nil {
		return payload, fmt.Errorf("cached input cost must be numeric")
	}
	payload.OutputCost, err = strconv.ParseFloat(f.value("output_cost"), 64)
	if err != nil {
		return payload, fmt.Errorf("output cost must be numeric")
	}
	payload.EnabledChatBot = f.boolValue("enabled_chat_bot")
	payload.VisionEnabled = f.boolValue("vision_enabled")
	payload.SetAsDefault = f.boolValue("set_default")
	payload.ProviderParams = f.providerParamsMap()
	return payload, nil
}

func (f *createForm) isEdit() bool {
	return f.mode == formModeEdit
}

func (f *createForm) targetID() int64 {
	return f.editingID
}

func (f *createForm) value(key string) string {
	for _, field := range f.fields {
		if field.Key == key {
			if field.Kind == fieldInput {
				return field.Model.Value()
			}
			break
		}
	}
	return ""
}

func (f *createForm) boolValue(key string) bool {
	for _, field := range f.fields {
		if field.Key == key && field.Kind == fieldBool {
			return field.BoolValue
		}
	}
	return false
}

func (f *createForm) providerParamsMap() map[string]interface{} {
	params := map[string]interface{}{}
	for _, field := range f.fields {
		if !field.IsProvider {
			continue
		}
		switch field.Kind {
		case fieldBool:
			params[field.Key] = field.BoolValue
		case fieldInput:
			val := strings.TrimSpace(field.Model.Value())
			if val != "" {
				params[field.Key] = val
			}
		case fieldSelect:
			params[field.Key] = field.SelectValue
		}
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func shortTokenizer(full string) string {
	parts := strings.Split(full, "::")
	return parts[len(parts)-1]
}

func providerSlug(entryID string) string {
	key := strings.ToLower(strings.TrimSpace(entryID))
	switch key {
	case "openrouter", "open_router":
		return "open_router"
	case "openai", "open_ai":
		return "open_ai"
	default:
		return key
	}
}

func buildProviderParamFields(provider string, meta ai.LLMMetadata, existing map[string]interface{}, defaults map[string]interface{}) []*formField {
	slug := providerSlug(strings.TrimSpace(provider))
	if slug == "" {
		return nil
	}
	specs, ok := meta.ProviderParams[slug]
	if !ok || len(specs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(specs))
	for name := range specs {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	var fields []*formField
	for _, name := range keys {
		spec := specs[name]
		label := strings.Title(strings.ReplaceAll(name, "_", " "))
		switch def := spec.(type) {
		case string:
			if def == "checkbox" {
				field := newBoolField(name, label, boolFromExisting(existing, name) || boolFromDefaults(defaults, name))
				field.IsProvider = true
				fields = append(fields, field)
			} else {
				field := newTextField(name, label, defaultString("", existing, defaults, name), false)
				field.IsProvider = true
				fields = append(fields, field)
			}
		case map[string]interface{}:
			switch strings.ToLower(stringValue(def["type"])) {
			case "checkbox":
				field := newBoolField(name, label, boolFromExisting(existing, name) || boolFromDefaults(defaults, name))
				field.IsProvider = true
				fields = append(fields, field)
			case "enum":
				val := stringFromExisting(existing, name)
				if val == "" {
					val = defaultString(stringValue(def["default"]), existing, defaults, name)
				}
				var opts []string
				if rawVals, ok := def["values"].([]interface{}); ok && len(rawVals) > 0 {
					for _, v := range rawVals {
						if s := stringValue(v); s != "" {
							opts = append(opts, s)
						}
					}
				}
				if len(opts) > 0 {
					field := newSelectField(name, label, val, opts)
					field.IsProvider = true
					fields = append(fields, field)
				} else {
					// Fallback to text field if no options
					field := newTextField(name, label, val, false)
					field.IsProvider = true
					fields = append(fields, field)
				}
			default:
				field := newTextField(name, label, defaultString("", existing, defaults, name), false)
				field.IsProvider = true
				fields = append(fields, field)
			}
		default:
			field := newTextField(name, label, defaultString("", existing, defaults, name), false)
			field.IsProvider = true
			fields = append(fields, field)
		}
	}
	return fields
}

func stringFromExisting(existing map[string]interface{}, key string) string {
	if existing == nil {
		return ""
	}
	if val, ok := existing[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case json.Number:
			return v.String()
		case fmt.Stringer:
			return v.String()
		case float64:
			return fmt.Sprintf("%g", v)
		case int:
			return fmt.Sprintf("%d", v)
		case int64:
			return fmt.Sprintf("%d", v)
		case bool:
			return strconv.FormatBool(v)
		default:
			return fmt.Sprint(v)
		}
	}
	return ""
}

func boolFromExisting(existing map[string]interface{}, key string) bool {
	if existing == nil {
		return false
	}
	if val, ok := existing[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "y":
				return true
			default:
				return false
			}
		case float64:
			return v != 0
		case int:
			return v != 0
		case int64:
			return v != 0
		}
	}
	return false
}

func boolFromDefaults(defaults map[string]interface{}, key string) bool {
	if defaults == nil {
		return false
	}
	if val, ok := defaults[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "y":
				return true
			}
		}
	}
	return false
}

func defaultString(fallback string, existing, defaults map[string]interface{}, key string) string {
	if val := stringFromExisting(existing, key); val != "" {
		return val
	}
	if defaults != nil {
		if value, ok := defaults[key]; ok {
			if s, ok := value.(string); ok && s != "" {
				return s
			}
		}
	}
	return fallback
}

func stringValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	case fmt.Stringer:
		return val.String()
	default:
		return fmt.Sprint(val)
	}
}

func defaultTokenizerFor(provider string, meta ai.LLMMetadata) string {
	target := "OpenAiTokenizer"
	switch provider {
	case "open_ai", "open_router":
		target = "OpenAiTokenizer"
	default:
		target = ""
	}
	for _, tok := range meta.Tokenizers {
		if target != "" && strings.Contains(tok.ID, target) {
			return tok.ID
		}
	}
	if len(meta.Tokenizers) > 0 {
		return meta.Tokenizers[0].ID
	}
	return "DiscourseAi::Tokenizer::OpenAiTokenizer"
}

func providerKeyHints(entryID string) []string {
	switch entryID {
	case "openrouter":
		return []string{"OPENROUTER_API_KEY", "OPENROUTER_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "anthropic":
		return []string{"ANTHROPIC_API_KEY"}
	default:
		return nil
	}
}

func joinUsage(usages []ai.LLMUsage) string {
	if len(usages) == 0 {
		return "not referenced"
	}
	names := make([]string, 0, len(usages))
	for _, u := range usages {
		if strings.TrimSpace(u.Type) != "" {
			names = append(names, u.Type)
		}
	}
	if len(names) == 0 {
		return "not referenced"
	}
	return strings.Join(names, ", ")
}

func firstNonEmpty(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(env[key]); val != "" {
			return val
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m aiConfigModel) renderLoadingScreen() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("212")).
		Padding(1, 0)

	spinnerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("212"))

	progressStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("246")).
		PaddingLeft(2)

	lines := []string{
		titleStyle.Render("🚀 Discourse AI Configuration"),
		"",
		spinnerStyle.Render(m.spinner.View() + " Initializing..."),
		"",
	}

	if len(m.loadingProgress) > 0 {
		for _, step := range m.loadingProgress {
			lines = append(lines, progressStyle.Render("✓ "+step))
		}
	}

	content := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("212")).
		Padding(2, 4).
		Width(60)

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		box.Render(content),
	)
}

func (m aiConfigModel) initLoadCmd() tea.Cmd {
	client := m.client
	ctx := m.ctx
	env := m.env
	cacheDir := m.cacheDir

	return func() tea.Msg {
		// Step 1: Enable AI features
		if err := client.EnableFeatures(ctx, aiFeatureSettings); err != nil {
			return aiInitCompleteMsg{err: fmt.Errorf("enable features: %w", err)}
		}

		// Step 2: Fetch state from Discourse
		state, err := client.FetchState(ctx)
		if err != nil {
			return aiInitCompleteMsg{err: fmt.Errorf("fetch state: %w", err)}
		}

		// Step 3: Load provider catalog
		catalog, err := providers.LoadCatalog(ctx, providers.CatalogOptions{
			CacheDir: cacheDir,
			Env:      env,
		})
		if err != nil {
			// Non-fatal, just log the warning
			catalog = ai.ProviderCatalog{}
		}

		return aiInitCompleteMsg{
			state:   state,
			catalog: catalog,
		}
	}
}
