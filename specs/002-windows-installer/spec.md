# Feature Specification: Windows Installer for MCPProxy

**Feature Branch**: `002-windows-installer`
**Created**: 2025-11-13
**Status**: Draft
**Input**: User description: "Required to add windows installer. It must install core and tray app, similar to macos installer. Also if possible add option start mcpproxy after installation is done. mcpproxy-tray can launch core app, required to add mcpproxy binary into PATH so user would be able to run cdm commands. For info page in windows installer u can use files from macos but adapt it for windows. installer build process must be added into CI workflow, installer must be availible as artefact on release page. I don't have any certs or .. to sign binaries for windows. Suggest me easy way to have it, but for now let's use it without sign. Suggest me way for testing installer. I have windows inside VM locally. But suggest a way to fix rebuild installer without generating new release on main branch."

## Clarifications

### Session 2025-11-13

- Q: When a user installs a newer version of MCPProxy over an existing installation, how should the installer handle the upgrade? → A: In-place upgrade - Replace binaries but preserve all user configuration and data in %USERPROFILE%\.mcpproxy
- Q: What is the minimum disk space requirement that should be displayed on the welcome screen and validated during installation? → A: 100 MB total
- Q: What is the specific minimum Windows version to support? → A: Windows 10 version 21H2 (November 2021 Update) or later, plus Windows 11 all versions
- Q: When adding the installation directory to PATH, should it be added to the system-level PATH or user-level PATH? → A: System-level PATH - All users on the machine can access `mcpproxy` command

## User Scenarios & Testing

### User Story 1 - Basic Windows Installation (Priority: P1)

A Windows user downloads the MCPProxy installer from the GitHub releases page and runs it to install both the core server and tray application on their system, making the tool immediately usable through either the GUI or command line.

**Why this priority**: This is the foundational installation capability that all other features depend on. Without a functional installer, Windows users cannot use MCPProxy at all.

**Independent Test**: Can be fully tested by downloading the installer artifact, running it on a clean Windows machine, and verifying both binaries are installed and the tray application can be launched from the Start Menu.

**Acceptance Scenarios**:

1. **Given** a Windows 10 version 21H2 or later (or Windows 11) machine with no prior MCPProxy installation, **When** user downloads and runs the installer, **Then** both mcpproxy.exe and mcpproxy-tray.exe are installed to the correct Program Files location
2. **Given** the installer is running, **When** user proceeds through all installation steps, **Then** installation completes without errors and creates a Start Menu entry for the tray application
3. **Given** installation completed successfully, **When** user opens Command Prompt and types `mcpproxy --help`, **Then** the command executes successfully showing help output
4. **Given** installation completed successfully, **When** user launches the tray application from Start Menu, **Then** the system tray icon appears and core server starts automatically

---

### User Story 2 - PATH Configuration for CLI Access (Priority: P1)

A Windows user who installed MCPProxy can immediately use the `mcpproxy` command from any Command Prompt or PowerShell window without manually configuring environment variables.

**Why this priority**: This is critical for usability. Windows users expect professional installers to handle PATH configuration automatically. Manual PATH setup creates friction and support burden.

**Independent Test**: Can be tested independently by opening a new Command Prompt after installation and running `mcpproxy --version` without any manual configuration.

**Acceptance Scenarios**:

1. **Given** MCPProxy installer is running, **When** installation completes, **Then** the installation directory is added to the system-level PATH environment variable (accessible to all users)
2. **Given** installation completed with PATH configured, **When** any user opens a new Command Prompt window, **Then** `mcpproxy` command is recognized and executes without "command not found" error
3. **Given** PATH is configured, **When** user runs `mcpproxy serve` from any directory, **Then** the core server starts successfully
4. **Given** a previous version was installed, **When** user installs a new version, **Then** PATH is updated correctly without duplicate entries

---

### User Story 3 - Post-Installation Launch Option (Priority: P2)

A Windows user completing the MCPProxy installation sees a checkbox option to launch the tray application immediately upon clicking Finish, allowing them to start using the tool without additional steps.

**Why this priority**: This improves first-run experience but is not essential for core functionality. Users can manually launch the app if this option is not available.

**Independent Test**: Can be tested by running the installer, checking the "Launch MCPProxy" checkbox at the final screen, and verifying the tray application starts immediately after clicking Finish.

**Acceptance Scenarios**:

1. **Given** installer reaches the completion screen, **When** the screen is displayed, **Then** a checkbox labeled "Launch MCPProxy Tray" is visible and checked by default
2. **Given** the launch checkbox is checked, **When** user clicks Finish, **Then** the tray application starts automatically and appears in the system tray
3. **Given** the launch checkbox is unchecked, **When** user clicks Finish, **Then** the installer closes without launching the tray application
4. **Given** the installer launches the tray app, **When** tray app starts, **Then** core server is automatically started by the tray application

---

### User Story 4 - Informational Screens (Priority: P3)

A Windows user running the MCPProxy installer sees welcome and completion screens with relevant information about what will be installed, system requirements, and quick start instructions tailored for Windows.

**Why this priority**: Professional appearance and user guidance are important but not blocking. The installer can function without these screens.

**Independent Test**: Can be tested by running the installer and reading through the welcome and conclusion screens to verify all information is accurate and Windows-specific.

**Acceptance Scenarios**:

1. **Given** installer starts, **When** the welcome screen appears, **Then** it displays product description, system requirements (Windows 10 version 21H2 or Windows 11, 100 MB disk space, port 8080 available), and what will be installed
2. **Given** installation completes, **When** the completion screen appears, **Then** it shows quick start instructions with Windows-specific paths (e.g., %USERPROFILE%\\.mcpproxy instead of ~/.mcpproxy)
3. **Given** welcome screen is displayed, **When** user reads "What Will Be Installed" section, **Then** it lists mcpproxy-tray.exe, mcpproxy.exe CLI, and Windows-specific components
4. **Given** completion screen is displayed, **When** user reads Quick Start section, **Then** instructions use Windows conventions (Command Prompt commands, Windows paths)

---

### User Story 5 - CI/CD Automation and Release Artifacts (Priority: P1)

The development team pushes a new tag to the main branch, triggering an automated workflow that builds the Windows installer and uploads it to the GitHub release page as a downloadable artifact alongside other platform releases.

**Why this priority**: Automated builds are essential for maintainability and ensuring consistent, reproducible releases. Manual installer building is error-prone and time-consuming.

**Independent Test**: Can be tested by creating a test tag on a feature branch, verifying the workflow runs, and checking that the installer artifact is generated and available for download.

**Acceptance Scenarios**:

1. **Given** a version tag is pushed to the repository, **When** the release workflow runs, **Then** Windows installer is built for both amd64 and arm64 architectures
2. **Given** the workflow completes successfully, **When** checking GitHub releases page, **Then** installer artifacts are attached with clear naming (e.g., mcpproxy-v1.0.0-windows-amd64-installer.exe)
3. **Given** the workflow is running, **When** installer build step executes, **Then** it bundles both mcpproxy.exe and mcpproxy-tray.exe with correct version information
4. **Given** multiple platform builds are running, **When** all complete, **Then** Windows installer appears alongside macOS DMG and Linux archives

---

### User Story 6 - Testing Without Main Branch Release (Priority: P2)

A developer working on installer improvements can test the installer on their local Windows VM by building it locally or triggering a prerelease workflow without creating a full production release on the main branch.

**Why this priority**: Essential for development workflow but not blocking for end users. Developers need efficient iteration cycles.

**Independent Test**: Can be tested by running the local build script on a development branch, copying the resulting installer to a Windows VM, and installing it to verify functionality.

**Acceptance Scenarios**:

1. **Given** a developer has local changes to installer scripts, **When** they run a local build command, **Then** an installer is generated that can be tested immediately
2. **Given** changes are pushed to the "next" branch, **When** the prerelease workflow runs, **Then** installer artifacts are generated and available for download from workflow runs
3. **Given** a prerelease installer is downloaded, **When** tested on Windows VM, **Then** it installs successfully and includes version information showing it's a prerelease (e.g., v1.0.0-next.abc123)
4. **Given** a developer needs to iterate on installer, **When** they rebuild locally, **Then** previous installation can be uninstalled cleanly before testing new version

---

### Edge Cases

- What happens when port 8080 is already in use during installation? (Installer should complete successfully; runtime will detect and handle port conflicts)
- How does the installer handle insufficient disk space? (Windows Installer framework will display error if less than 100 MB available before installation begins)
- What happens if user cancels installation midway? (Windows Installer framework automatically rolls back partial installation)
- How does the installer behave when upgrading from a previous version? (Performs in-place upgrade: replaces binaries while preserving all user configuration and data in %USERPROFILE%\.mcpproxy)
- What happens if another instance of mcpproxy is running during installation? (Installer should detect running processes and prompt user to close them)
- How does uninstallation work? (Standard Windows "Add or Remove Programs" entry should cleanly remove binaries and Start Menu entries, but preserve user data)
- What happens on systems with restrictive antivirus or security software? (Unsigned binaries may trigger warnings; installer should provide clear guidance)
- How does the installer handle systems without administrator privileges? (Installation should fail with clear message requiring admin rights for Program Files installation and system-level PATH modification)

## Requirements

### Functional Requirements

- **FR-001**: Installer MUST bundle both mcpproxy.exe (core server) and mcpproxy-tray.exe (GUI application) in a single installable package
- **FR-002**: Installer MUST install binaries to a standard Windows location (e.g., Program Files\MCPProxy)
- **FR-003**: Installer MUST add the installation directory to the system-level PATH environment variable automatically (accessible to all users)
- **FR-004**: Installer MUST create a Start Menu entry for launching the tray application
- **FR-005**: Installer MUST provide an option to launch the tray application immediately after installation completes
- **FR-006**: Installer MUST display a welcome screen with product information, system requirements (Windows 10 version 21H2 or Windows 11, 100 MB disk space), and installation overview
- **FR-007**: Installer MUST display a completion screen with quick start instructions and relevant Windows-specific paths
- **FR-008**: Installer MUST support both Windows amd64 and arm64 architectures
- **FR-009**: Installer MUST embed version information matching the release tag
- **FR-010**: Installer MUST register itself in Windows "Add or Remove Programs" for proper uninstallation
- **FR-011**: CI workflow MUST automatically build Windows installers when version tags are pushed to main branch
- **FR-012**: CI workflow MUST upload installer artifacts to GitHub releases page with clear naming convention
- **FR-013**: Prerelease workflow on "next" branch MUST generate installer artifacts for testing without creating production releases
- **FR-014**: Local build scripts MUST allow developers to generate installers for local testing
- **FR-015**: Installer MUST work without code signing initially (unsigned distribution for testing phase)
- **FR-016**: Welcome screen content MUST be adapted from macOS installer with Windows-specific terminology and paths
- **FR-017**: Completion screen MUST use Windows conventions (Command Prompt commands, %USERPROFILE% paths instead of ~/.mcpproxy)
- **FR-018**: Installer MUST support silent/unattended installation mode for enterprise deployments
- **FR-019**: Installer MUST perform in-place upgrades when a previous version exists, replacing binaries while preserving all user configuration and data in %USERPROFILE%\.mcpproxy
- **FR-020**: Installer MUST require administrator privileges for installation to Program Files and system-level PATH modification

### Key Entities

- **Windows Installer Package (.exe or .msi)**: Self-contained executable or MSI file that bundles all necessary components and installation logic for Windows deployment
- **Core Binary (mcpproxy.exe)**: Command-line server application that provides the MCP proxy functionality
- **Tray Binary (mcpproxy-tray.exe)**: GUI application that manages the core server and provides system tray interface
- **Installation Directory**: Default location in Program Files where binaries and resources are installed
- **System PATH**: System-level Windows environment variable that enables command-line access to mcpproxy from any directory for all users
- **Start Menu Entry**: Windows shortcut that allows users to launch the tray application from the Start Menu
- **Informational Screens**: RTF-formatted welcome and conclusion screens displayed during installation (adapted from macOS versions)
- **CI Workflow Artifact**: Build output from GitHub Actions that can be downloaded from releases page or workflow runs
- **Prerelease Build**: Installer generated from "next" branch for testing, tagged with commit hash (e.g., v1.0.0-next.abc123)

## Success Criteria

### Measurable Outcomes

- **SC-001**: Windows users can download a single installer file and complete installation in under 3 minutes without manual configuration
- **SC-002**: After installation, users can successfully run `mcpproxy --help` from a new Command Prompt window without PATH errors
- **SC-003**: Tray application launches successfully from Start Menu and automatically starts the core server within 10 seconds
- **SC-004**: CI workflow automatically generates installer artifacts within 15 minutes of pushing a version tag
- **SC-005**: Installer artifacts appear on GitHub releases page with correct version naming and are downloadable by end users
- **SC-006**: Developers can rebuild and test installers locally within 5 minutes without triggering production releases
- **SC-007**: 95% of Windows users complete installation successfully on first attempt without support intervention
- **SC-008**: Installer works correctly on Windows 10 version 21H2 or later and Windows 11 across amd64 and arm64 architectures
- **SC-009**: Uninstallation removes all binaries and Start Menu entries cleanly, verified by absence in "Add or Remove Programs"

## Commit Message Conventions

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: add Windows installer with NSIS/WiX tooling

Related #[issue-number]

Implement Windows installer generation using [NSIS/WiX] to bundle
mcpproxy.exe and mcpproxy-tray.exe for distribution. Installer handles
PATH configuration, Start Menu shortcuts, and post-install launch option.

## Changes
- Add installer build scripts for Windows (.nsi or .wxs definition)
- Integrate installer generation into release and prerelease workflows
- Adapt macOS informational screens (RTF) to Windows conventions
- Add local build instructions for testing installers

## Testing
- Tested on Windows 10 amd64 and Windows 11 arm64 VMs
- Verified PATH configuration and CLI access
- Confirmed tray application launches and starts core server
- Validated uninstallation removes all components cleanly
```
