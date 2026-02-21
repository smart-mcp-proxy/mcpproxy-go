# TUI End-to-End Testing

## Overview

The TUI module includes a comprehensive E2E test suite covering complete user interaction workflows. These tests verify that individual components (handlers, renderers, state management) work correctly together in realistic usage scenarios.

## Test Coverage

**File**: `internal/tui/e2e_test.go`
**Tests**: 18 end-to-end workflow tests
**Coverage**: 87.5% of statements
**All tests pass with `-race` flag** for concurrency safety

## Test Categories

### Navigation & Cursor Movement
- **TestE2ECursorNavigation** - Tests j/k key navigation with boundary checks
- **TestE2ESequentialKeyPresses** - Tests handling multiple key presses in sequence

### Filtering
- **TestE2EFilterWorkflow** - Complete filter mode workflow (enter, navigate, exit)
- **TestE2EClearFiltersWorkflow** - Clearing all active filters
- **TestE2EFilterSummaryDisplay** - Filter badges display in view
- **TestE2EMultipleFiltersApply** - Applying multiple filters simultaneously

### Sorting
- **TestE2ESortWorkflow** - Complete sort mode workflow with indicators
- **TestE2ETabbedSortingByTab** - Sort columns and rendering by tab

### Tab Management
- **TestE2ETabSwitching** - Switching between servers/activity tabs and state preservation

### OAuth
- **TestE2EOAuthRefreshWorkflow** - OAuth refresh trigger via 'o' key

### Display & Rendering
- **TestE2EHealthStatusDisplay** - Health indicator rendering (●, ◐, ○)
- **TestE2EHelpDisplay** - Tab-aware help text display
- **TestE2ELongServerNames** - Name truncation for long names
- **TestE2EResponseToWindowResize** - Terminal size change handling
- **TestE2EEmptyState** - Empty list behavior

### Commands
- **TestE2EQuitCommand** - Quit ('q') command
- **TestE2ERefreshCommand** - Refresh ('r') command

## Running Tests

### All E2E tests
```bash
go test ./internal/tui/... -v -run E2E -race
```

### All TUI tests (unit + E2E)
```bash
go test ./internal/tui/... -race
```

### Coverage report
```bash
go test ./internal/tui/... -cover
```

### Verbose output
```bash
go test ./internal/tui/... -v -race
```

## Test Structure

Each E2E test follows this pattern:

1. **Setup** - Create model with test data
2. **Execute** - Simulate user interactions (key presses)
3. **Verify** - Assert expected state and rendering output

### Example: Filter Workflow
```go
// Create model with servers
m := NewModel(context.Background(), client, 5*time.Second)
m.servers = []serverInfo{ ... }

// Enter filter mode (press 'f')
result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
m = result.(model)

// Verify state
assert.Equal(t, ModeFilterEdit, m.uiMode)
```

## Key Testing Insights

### Model Updates
- The `Update()` method returns `(tea.Model, tea.Cmd)`
- Must type-assert back to `model`: `m = result.(model)`
- Commands are typically nil for these tests (real commands execute in Bubble Tea)

### State Transitions
- Pressing Escape in filter mode resets cursor to 0
- Filters are cleared when exiting filter mode
- Tab switching preserves cursor position per tab

### Rendering
- `renderServers()` and `renderActivity()` require height parameter for visible rows
- Health indicators and filter badges are included in view output
- Names are truncated to fit column width

### Mode System
The TUI has 5 modes:
- **ModeNormal** - Navigation mode
- **ModeFilterEdit** - Filter editing mode
- **ModeSortSelect** - Sort selection mode
- **ModeSearch** - Search mode (optional)
- **ModeHelp** - Help mode (optional)

## Integration with CI

These tests run as part of the standard test suite:
```bash
go test -race ./internal/tui/...
```

No additional dependencies are required—tests use only Go standard library and existing test frameworks (testify).

## Future Enhancements

Potential areas for additional E2E tests:
1. Search mode workflow (when implemented)
2. Help mode details (when implemented)
3. Performance testing with large server lists
4. Unicode/emoji handling edge cases
5. Accessibility features testing

## Debugging Failed E2E Tests

1. **Check test output** - Detailed assert messages show expected vs actual
2. **Add debug prints** - Use `t.Logf()` to print model state
3. **Verify handlers** - Check `internal/tui/handlers.go` for key handling logic
4. **Check renders** - Verify `internal/tui/views.go` for display logic
5. **Run individual test** - Use `-run TestName` to isolate and debug

## References

- **Bubble Tea Framework**: https://github.com/charmbracelet/bubbletea
- **TUI Architecture**: See `internal/tui/model.go` for state management
- **Handler Logic**: See `internal/tui/handlers.go` for key bindings
- **Rendering**: See `internal/tui/views.go` for display logic
