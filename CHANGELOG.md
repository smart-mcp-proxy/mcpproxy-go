# Changelog

All notable changes to [MCPProxy](https://mcpproxy.app) are documented here.
Releases follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Bug Fixes

- **homebrew:** One-line install in docs + guard tap job against pre-release tags (#486) ([#486](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/486)) ([`1098701`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/109870116fe17aa1ce7ecfd603962c1d3de21ba0))

### CI/Build

- **053:** Cosign keyless signing of release checksums (WP-C1) ([`aaa78a8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/aaa78a8b79d3d27c594aa492bc6d2f2532d7d803))
- **053:** Generate + attach SPDX SBOM to releases (WP-C2) ([`a86bd56`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a86bd56a6d56784b8a104ab2c062bb9af1e8f7ac))
- **053:** Add SLSA build provenance for release artifacts (WP-C3) ([`db0bf35`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/db0bf35624d295befc3c4284035f0ccd64dd83ab))

### Documentation

- **051:** README hero — frosted-tiles banner + demo GIF (#488) ([#488](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/488)) ([`25731da`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/25731da5e1a26753ee90a173a8ea03a317e82666))

## [0.33.1] - 2026-05-20

### Bug Fixes

- **registry/483:** Docker MCP Catalog → docker run install (no fake docker:// URL) (#484) ([#484](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/484)) ([`f772e49`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f772e493f6dd7a60704c7c3bd36a92c3e29f5b73))
- **diagnostics:** Classify HTTP timeouts and string-wrapped 5xx as known codes (#469) ([#469](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/469)) ([`fd78619`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fd786197dd295a310350888aa4754604de56d790))
- **upstream:** Require N consecutive health-check timeouts before marking Error (#470) ([#470](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/470)) ([`0cbdb89`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0cbdb89975cf2d2e2e261865a1519fe3bcc92a68))
- **restart:** Re-read mcp_config.json before restarting upstream (#467) (#471) ([#471](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/471)) ([`5a1718a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5a1718ae783c3dd2acf646be5296d2f080d6c3e7))

### Documentation

- Add release runbook covering 6 SPOFs (S0-3) (#403) ([#403](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/403)) ([`1356c60`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1356c60fc9fca6a27070a34bbfed07e33eeb6888))

## [0.33.0] - 2026-05-19

### Features

- **050:** Global Tools overview page + CLI parity (#437) (#481) ([#481](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/481)) ([`538c1a9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/538c1a9a6e555b4b644a0eb0df4e7362e83d2a6d))

## [0.32.1] - 2026-05-19

### Bug Fixes

- **truncate:** Persist + recursively cache truncated payloads so read_cache pagination works (#480) ([#480](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/480)) ([`95d09b0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/95d09b00a04d5ec95384fcc86c36a475570d9bd3))
- **truncate:** Unique cache keys per payload + read_cache truncation observability (#482) ([#482](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/482)) ([`496349c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/496349c75c8cfdcb2323e21ac89dd6f44532e953))

## [0.32.0] - 2026-05-18

### Bug Fixes

- **049:** ClassifyServerToolStatus must use runtime config authority (#477) ([#477](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/477)) ([`ff03db9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ff03db9214d80bceb20c3fc84840571f2e9c612d))
- **ci:** Eliminate chronic E2E timeout — Runtime.Close Docker-verify waste (#478) ([#478](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/478)) ([`70bf68c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/70bf68c469f8437b6c45dde6a5ed736011e9ae95))
- **configsvc:** Deep-copy tool-filter slices in Snapshot.Clone (+ regression guard) (#479) ([#479](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/479)) ([`3cafa5a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3cafa5a2116ddb99c4034f435ecd70badb178af6))

### Features

- **config:** Layered config tool filter — config_denied over user toggles (#468) ([#468](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/468)) ([`3ffa41e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3ffa41e90f2ab5c94ef6f912bedabf948d92c9af))
- **mcp:** Agent-discoverable disabled tools (spec 049) (#476) ([#476](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/476)) ([`95f5fa4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/95f5fa4a05cecf0bace65a7bbaf2cc114cda4fc6))

## [0.31.1] - 2026-05-17

### Bug Fixes

- **embed:** Untrack web/frontend/dist/* and serve a fallback stub (#473) ([#473](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/473)) ([`7a1f6fd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7a1f6fd21d729b4ae730936f9d411ee388fc71ad))
- **generate-types:** Re-sync TS output + CRLF-safe drift test (supersedes #472) (#475) ([#475](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/475)) ([`0afa3fb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0afa3fb940ea2ba801937422d7ab89bc07ea0a4f))

## [0.31.0] - 2026-05-14

### Bug Fixes

- **diagnostics:** Correct bug-report URL in Unknown failure catalog entry (#465) ([#465](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/465)) ([`aaec117`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/aaec117f86c6c7d9551b563cb50ff8c3dc50776f))
- **upstream:** Stop misclassifying transport errors as auth failures (#464) ([#464](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/464)) ([`0597762`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/05977624a46445770d6fca111e51edf293af1c00))

### Features

- **webui:** Per-tool enable/disable + bulk Enable All/Disable All (#463) ([#463](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/463)) ([`c086770`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c0867702fc07795d22effcf6dfc6105001ef363b))
- **server-detail:** Display + edit headers/env, with reveal & convert-to-secret (#466) ([#466](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/466)) ([`d27fa38`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d27fa389978075416d67b9e2355278601ff04193))

## [0.30.1] - 2026-05-13

### Bug Fixes

- **ui:** Respect engaged flag in sidebar Setup pulse (#462) ([#462](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/462)) ([`4b4b62a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4b4b62a43739dca57cb5296943a5ef69b555014b))

### Documentation

- **installation:** Add migrating-from-manual-install section (#459) ([#459](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/459)) ([`24aab3d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/24aab3d1c9cdb1e8be604323c8baf6015a8f6f92))

### Features

- **doctor:** Surface snap-docker override hint when host needs it (#460) ([#460](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/460)) ([`9b79254`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9b7925442cf9198cb636c034657f98884427d7f2))

### packaging

- **deb:** Ship unattended-upgrades whitelist so installs auto-update (#458) ([#458](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/458)) ([`be927b6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/be927b6bc62077041e2073796161f6188be1ff5a))

## [0.30.0] - 2026-05-12

### Bug Fixes

- **oauth:** Treat zero ExpiresAt as never-expires in HasPersistedToken (#453) ([#453](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/453)) ([`a11d002`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a11d0029503a725523b6b5f2a4501e70070eca71))
- **046:** Persist LauncherWaitTimeout to BBolt + regenerate OAS (#454) ([#454](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/454)) ([`45a06d9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/45a06d9912f69b3b7e0eeb35fc99a9e1f7a2e80a))

### Features

- **launcher:** Local launcher for HTTP/SSE upstreams (spec 046) (#452) ([#452](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/452)) ([`31e4887`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/31e4887b6917d7b29f387c9d7c89aafa24139391))
- **telemetry:** Phase H — v3 diagnostics counters (spec 044) (#449) ([#449](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/449)) ([`f4ad4e7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f4ad4e7c36ec8af24325f41453e9c88db8a9afde))

## [0.29.5] - 2026-05-08

### Features

- **048:** Eliminate remaining tray /api/v1/servers refetches (#451) ([#451](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/451)) ([`6a5d39d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6a5d39d4a262c05f6a1806135f9d68f6fc560403))

## [0.29.4] - 2026-05-08

### Bug Fixes

- **wizard:** Record per-step completion/skip for Spec 046 funnel (#448) ([#448](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/448)) ([`09b7b36`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/09b7b36f0eb1b8c36321c7f88abf99f55eae169a))

### Features

- **047:** Cut idle CPU 92% — scanner cache + SSE payload embed + coalescer (#450) ([#450](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/450)) ([`eae45ef`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/eae45ef4027384d439889012ab9c2a0fc483741e))

## [0.29.3] - 2026-05-05

### Bug Fixes

- **release-notes:** Stop inventing edition scoping in generated notes (#445) ([#445](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/445)) ([`4ba237d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4ba237db13c5f0a968cb3885f081f4afd2c68649))

## [0.29.2] - 2026-04-30

### Bug Fixes

- **ui/store:** Clear missing server fields on merge (#438) (#443) ([#443](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/443)) ([`b541321`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b541321a83390c2e3e691af5965dd3dabb1f8f06))
- **runtime:** Emit servers.changed after tool approval (#438) (#444) ([#444](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/444)) ([`3e4d895`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3e4d895fc1d08fb79c3436a3a30bb4dcb6d3b06f))

### Features

- **ui:** Clickable KPI cards on Activity / Servers / Agent Tokens (#436) (#442) ([#442](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/442)) ([`f9c8d0e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f9c8d0e9fddf58798caaaa3da2885531deee290f))

## [0.29.1] - 2026-04-30

### Bug Fixes

- **upstream:** Docker recovery survives launchd minimal PATH + stale state (#441) ([#441](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/441)) ([`46a52da`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/46a52dab520fede0b2228f116270fd5f670325a5))

## [0.29.0] - 2026-04-30

### Bug Fixes

- **release:** Replace --no-progress with --quiet in apt/rpm publish ([`e9ae7b6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e9ae7b6d54a9a6a863a6321a43a59b3a9ed4fb01))

### Features

- **046:** Adaptive onboarding wizard v2 with sidebar Setup, sectioned import, passive Verify (#433) ([#433](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/433)) ([`0064d88`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0064d8860a3a0043b8b5109c63d5d82ebcf85ab0))
- **046-us3:** Onboarding-funnel telemetry — payload fields + privacy guards (#434) ([#434](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/434)) ([`738c90a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/738c90a3f9f7bca6324bd02979761c18c1ac0683))

## [0.28.1] - 2026-04-30

### Bug Fixes

- **secureenv:** Inherit user's interactive PATH so stdio servers find uvx/npx (closes #439) (#440) ([#440](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/440)) ([`2e47b9c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2e47b9ced302de6e98941ddcd74bf2759f3e5f67))

## [0.28.0] - 2026-04-28

### Documentation

- Add Arch Linux AUR install instructions ([#430](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/430)) ([`adbf58f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/adbf58f47a4175722e304c6d6abf82f1e2058a58))
- **install:** Add Arch Linux (AUR) install instructions (#431) ([#431](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/431)) ([`0b8402f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0b8402fea813d6711d642f12c16b31744410520f))

## [0.27.1] - 2026-04-27

### Bug Fixes

- **diagnostics:** Point error docs links to docs.mcpproxy.app (#429) ([#429](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/429)) ([`2b9b5f9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2b9b5f985ad998b18be919158d44ac7e6b98da2c))

## [0.27.0] - 2026-04-26

### Bug Fixes

- **scanner:** Resolve docker via shellwrap in source extraction path (#421) ([#421](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/421)) ([`5200185`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/52001850a7078ee0eab0fd5e83a3d60136b5201b))
- **cli:** Expand security scan report output (#423) ([#423](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/423)) ([`c341f29`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c341f29c25d21979206967ea9e8ca8fab4b52d0c))
- Normalize nil CallTool arguments to empty object (#427) ([#427](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/427)) ([`08d6bb8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/08d6bb8ff8c3ca4554c9a232ef2e76ea65ad73d4))

### Features

- **webui:** Bring Server Config tab to parity with macOS tray (#424) ([#424](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/424)) ([`9e530c4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9e530c480aa8c87323a312ba4905ed86573f3ec3))
- **webui:** Scanned-files viewer in scan report (#426) ([#426](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/426)) ([`75af2a8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/75af2a851869122281a37b4ca0be1cf2cc17680f))

### security

- **headers:** Redact sensitive header values in upstream_servers list and /api/v1/servers (#425) ([#425](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/425)) ([`2566e24`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2566e249f281e47ee297afef0daeb89c573f6c9a))

## [0.26.3] - 2026-04-26

### Bug Fixes

- **installer+docker:** Launch tray with clean env, harden docker probe (#420) ([#420](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/420)) ([`f010ffb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f010ffb8e87e3a1ecfa54afe34b60b264ddf7e95))

## [0.26.2] - 2026-04-26

### Bug Fixes

- **macos-tray:** Secrets page blanks the entire window after #418 (#419) ([#419](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/419)) ([`72808db`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/72808dbdaacf9beda6f2d6312dc82f6695825222))

## [0.26.1] - 2026-04-26

### Bug Fixes

- **macos-tray:** Show "Loading…" instead of "No servers configured" during cold start (#417) ([#417](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/417)) ([`344516f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/344516f31368d21d53680a6d3abd7b634c963d77))
- **macos-tray:** Make secrets config-first workflow visible after Add Secret (#418) ([#418](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/418)) ([`a4ae827`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a4ae827bfef917de80d81de83260142d7d4820c7))

### CI/Build

- **release:** Pass VERSION/COMMITS via env to avoid shell injection ([`678640e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/678640eab68014b1bbb6635b4e105c6fa2b2657a))

## [0.26.0] - 2026-04-26

### Bug Fixes

- **webui:** Prevent Scan Now tooltip clipping at right edge (#407) ([#407](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/407)) ([`907815a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/907815ad812e23173585f99b950caeb9c407bf66))
- **macos-pkg:** Remove misleading non-auto-starting LaunchAgent (#405) (#412) ([#412](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/412)) ([`bb34b13`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/bb34b1347c574103d5c4ea24b60787da8bce0f05))
- **macos-tray:** Pass MCPPROXY_KEYRING_WRITE to bundled core (#409) (#413) ([#413](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/413)) ([`b16ffc7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b16ffc71af24718a4bcaaf4ae289d384236bf1a4))
- **mcp:** Make lethal trifecta warning opt-in (#406) (#414) ([#414](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/414)) ([`7a5f0e0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7a5f0e0fd3702c64115bb976453861f6d1f0488f))
- **mcp:** Set explicit annotation hints on all internal tools (#415) ([#415](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/415)) ([`d6b913e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d6b913eb1a1b71bc243b383c21329770f1b88aa4))

### Features

- **macos-tray,api:** Expose resolved isolation defaults + per-field clear (#408) ([#408](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/408)) ([`475a95e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/475a95e82020986869670331d45520d4c5863023))
- **cockpit:** Paperclip goal cockpit spec 045 + agent instruction drafts (#411) ([#411](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/411)) ([`d63e81e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d63e81eccabec5a260406dfa6f04c085d7b34892))

## [0.25.0] - 2026-04-24

### Bug Fixes

- **macos-tray:** Preserve bool fields on server PATCH + refresh after save (#399) ([#399](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/399)) ([`74f2bfd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/74f2bfd810cf7bce00fea9c44ade72acf4b4ff33))
- **docker:** Probe detects Docker via full path when PATH is minimal (#404) ([#404](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/404)) ([`7619f0a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7619f0ab933dc310746d9fe48cdb2a08dc1d6de4))

### Features

- **telemetry:** Schema v3 extension — env_kind, activation funnel, autostart (spec 044) (#401) ([#401](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/401)) ([`ebcbfcc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ebcbfcc60ff05d01b52a9e42ad040b7932398c87))
- **diagnostics:** Stable error-code catalog with REST + CLI surfacing (#400) ([#400](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/400)) ([`1a0646f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1a0646f894df2cfa4de574d8c559b8479ed69e9a))

## [0.24.9] - 2026-04-23

### Bug Fixes

- **config:** Remove stdout debug prints that corrupt stdio MCP stream ([`88fe74e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/88fe74e78c7af7c812c036a410ef18eb758319ef))

## [0.24.8] - 2026-04-23

### Bug Fixes

- **upstream/stdio:** Surface stderr + real reason on MCP init failure (#396) ([#396](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/396)) ([`41b778e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/41b778e65829c9efade20f2f34af5e37d249f2b8))

### Features

- **macos-tray+api:** Expose per-server docker isolation overrides (#397) ([#397](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/397)) ([`c200793`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c2007936f3c7aecdf2b5c5de5de14b7a450093c2))
- **webui:** Show version + check-for-updates below sidebar logo (#392) (#398) ([#398](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/398)) ([`c786fed`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c786fed0451a477a721d83eb2daad9301c2e52c1))

## [0.24.7] - 2026-04-23

### Bug Fixes

- **docker-isolation:** Fix probe caching, warn on ignored opt-ins, add UI toggle (#395) ([#395](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/395)) ([`2dafed2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2dafed22d317d3076fe78b32e7a03b484cc5350d))

### Features

- **release:** Publish linux apt/yum repos to Cloudflare R2 (#394) ([#394](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/394)) ([`0776a15`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0776a15a67e9429200669058eace0444122badcb))

## [0.24.6] - 2026-04-17

### Bug Fixes

- **ui+tray:** Tool quarantine diff as side-by-side before/after (#391) ([#391](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/391)) ([`debe26f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/debe26fee35b56f49bc6c2742b65f4d0769df933))

## [0.24.5] - 2026-04-17

### Bug Fixes

- **tray/macos:** Show correct latest version in update menu ([`b85e7db`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b85e7dbc8c697bbbf9fb1a567f1c52d1f58ac8d7))

## [0.24.4] - 2026-04-14

### Bug Fixes

- **telemetry:** Correct docker_status metric + schema v3 additions (#388) ([#388](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/388)) ([`6b6dcb3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6b6dcb333ee672262591f8328a61ef265b6f9050))

## [0.24.3] - 2026-04-13

### Bug Fixes

- **telemetry:** Normalize version string to always carry v prefix (#387) ([#387](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/387)) ([`f7b7b01`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f7b7b0134f036465de994242c26a9f10a7feabe8))

### Features

- Auto-clean deprecated config fields on startup (#384) ([#384](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/384)) ([`df12602`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/df1260215aa74f97a9703d5e2d5e7beb2458ae10))
- **packaging:** Ship .deb and .rpm Linux packages via nfpm (#385) ([#385](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/385)) ([`19b853a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/19b853a555f99e00d3a39a24e642b02d4688a666))
- **scanner:** Diagnose & opt out of no-new-privileges on snap-docker hosts (#386) ([#386](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/386)) ([`77c2b37`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/77c2b376270b5c80a712e89180ba809465cee129))

## [0.24.2] - 2026-04-11

### Bug Fixes

- **shellwrap:** Capture login-shell PATH so docker credential helpers resolve (#382) ([#382](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/382)) ([`e1c7e41`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e1c7e41578e064dbcdc82a733a550711838c0a46))

### Features

- **mcp:** Advertise native args object on call_tool_* variants (#379) ([#379](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/379)) ([`33f3348`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/33f334874283391d06e809cf9e3a02480184894d))
- **mcp:** Advertise maxResultSizeChars on every tool (#380) ([#380](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/380)) ([`e911e39`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e911e39512a05d08ee8a893dfd603cee69a71a32))

## [0.24.1] - 2026-04-11

### Bug Fixes

- **macos-tray:** Download DMG matching host CPU architecture (#377) ([#377](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/377)) ([`4dad6d1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4dad6d110e4e8f81cb1def95450696b38c23a8c9))
- **docker:** Wrap docker CLI invocations in user shell to inherit PATH (#378) ([#378](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/378)) ([`b6dc5ad`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b6dc5ade72eefe1a237d87d38baff2c2956c20d2))

## [0.24.0] - 2026-04-11

### Bug Fixes

- **#370:** Quarantine_enabled=false auto-approves new servers ([`98cd0b4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/98cd0b4e756187bdc667fda1087b4ef11510bdde))
- **039:** Resolve 13 QA findings in security scanner plugin system (#372) ([#372](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/372)) ([`3f975ab`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3f975ab973631357a5bf34e810a36a7d5b02758f))
- **042:** Emit enable_web_ui alongside enable_prompts in feature flags (#374) ([#374](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/374)) ([`f0c171f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0c171f4191d523c15f00f5fa237ac76adf42f3b))
- **telemetry:** Honor --config flag in enable/disable save path (#375) ([#375](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/375)) ([`bbd2a47`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/bbd2a473ddd13b9c37c1dc18d16c828075d5c44b))
- **scanner:** Route non-CVE findings out of Supply Chain Audit section ([`9100e38`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9100e387a020b6c314fbbc881aef13588098a68e))
- **release:** Auto-update Homebrew cask on release (#367) ([#367](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/367)) ([`12c2095`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/12c2095af27572ca5e7c7703d28a62dbce7cf8c2))

### Features

- **039:** Security Scanner Plugin System (#364) ([#364](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/364)) ([`60c2eef`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/60c2eefc67dc6166d252f73efd0570da4e8d7858))
- **042:** Telemetry tier 2 — privacy-respecting usage signals (#373) ([#373](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/373)) ([`668bd86`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/668bd867f4a51517865e46538319fb64b8e35705))
- **042:** Serve telemetry payload via REST, require daemon for show-payload (#376) ([#376](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/376)) ([`80afbd5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/80afbd5c7cb7ebab6b3b26cb952de8d68684378e))

## [0.23.2] - 2026-04-09

### Bug Fixes

- Preserve ImageContent and AudioContent blocks through proxy (#368) (#369) ([#369](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/369)) ([`4204b1e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4204b1ed15565498a9d25d48e12c16a556f831b6))

## [0.23.1] - 2026-04-02

### Bug Fixes

- Add missing GetToolApprovalStatus to test mocks and storage import ([`dec4f62`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dec4f6287d720fb6ab5f3798cbc3e51a3b280c3b))
- Windows-compatible connect tests (path prefix + file permissions) ([`ecbab79`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ecbab79c46fed1e639d73092bba27b4347a8095d))
- **tray:** Use configured port for Open Web UI (#362) ([#362](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/362)) ([`3770ece`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3770ece9885fc182a18b780e360d8198fd0e8fd0))

### Features

- **041:** Quarantine state machine invariants, property tests & security fix ([#360](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/360)) ([`b9375ed`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b9375edb8c2642de3b48b1e13e6b69ce60f9ba62))
- Add reconnect_on_use for on-demand upstream reconnection (#363) ([#363](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/363)) ([`d98458b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d98458bed9941317b290816bf1e474db5cebfd9c))

## [0.23.0] - 2026-04-01

### Bug Fixes

- All Needs Attention items navigate to server detail page ([`430e3ef`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/430e3ef99249ea202fd70e7a6484ec3bc33975cd))
- Docker status shows active when containers running + activity live updates ([`8db706e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8db706e8aa1868be87d2c8bd264d6006f514b5f1))
- Cap token savings at 99.99%, fix Docker status in Web UI ([`96cda77`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/96cda77fe4573124945a269d4799a12d12662e2c))
- Annotations returned by API + rebuild frontend into Go embed ([`42b7903`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/42b79038b12b0f5b2024d91ba628e2e57bd1af84))
- Changed tools were auto-approved on second checkToolApprovals pass ([`c61630c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c61630c83acacbb7e330e77ff7b4037198699765))

### Features

- Tool approval badges (new/changed), annotations, word-level diff ([`fa88993`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fa889936dc281087a0f076e5b4cbe8637d41a6eb))
- Enrich /tools API with approval_status, sort pending first ([`9b87a6f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9b87a6fb935257f6acbbdc393beaaea07d52d0bf))
- **macOS:** Individual tool approve, pending column, client name in activity ([`a0b232c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a0b232c9b507ca8a46c2ab66ab4a4c24b12844ae))

## [0.22.1-rc.8] - 2026-03-31

### Bug Fixes

- Add @executable_path/../Frameworks to rpath for Sparkle ([`a243114`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a243114243a7730aa0df5067451f7423f5815201))
- Test workflow path for Swift app bundle ([`6298090`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/62980907f857a62a41a236c628b46a0e10ac9331))
- Resolve OUTPUT_DIR to absolute path before cd in build script ([`ab51aa6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ab51aa6982c6afc02379faced0ddb787e75be85f))

## [0.22.1-rc.7] - 2026-03-30

### Bug Fixes

- Bundle Sparkle.framework in Swift app to prevent launch crash ([`3268a17`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3268a175205d75c798747d616e3bfc5240ae45fb))

## [0.22.1-rc.6] - 2026-03-30

### Bug Fixes

- Add CFBundleIconName + CFBundleIconFile to Info.plist, bundle .icns ([`0a7fa79`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0a7fa79c27d0a49d69b389a5de8440e3d0da8924))

## [0.22.1-rc.5] - 2026-03-30

### Bug Fixes

- Copy icon PNGs to app bundle Resources for tray icon ([`ddfd3cb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ddfd3cb0c4c218e929e57d4e92df68b93d7785cc))

## [0.22.1-rc.4] - 2026-03-30

### Bug Fixes

- Add fail-fast: false to release/prerelease matrix ([`3aef851`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3aef851b0f0a65c1fc63f11433947fc6fe16dadc))

## [0.22.1-rc.3] - 2026-03-30

### Bug Fixes

- Show MCPProxy in Launchpad + fix LaunchAgent executable path ([`54fe5aa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/54fe5aabebbc07d3e2765865c00ad8cfe0986b68))

## [0.22.1-rc.2] - 2026-03-30

### Bug Fixes

- **037:** Fix compilation errors in Swift tray app ([`c55150d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c55150d5ec448dc7d6d58c7e6d8dd6abcb6d15e6))
- **037:** Add local network usage description, remove server entitlement ([`f2a99fa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f2a99fa65fe0f7744b32fb77ba04c19910c0d59a))
- **037:** Start core on app launch, not on first menu click ([`cdb3200`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cdb320035705cee452f554a54ff2ecf36982ec0b))
- **037:** Probe socket with API call before attaching to external core ([`dd60bc6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dd60bc6b0bf53f176b91e7a7593501a067c4290d))
- **037:** Use TCP for SSE stream, socket for API calls ([`6c81e16`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6c81e16cb84d9ddc54d2f3520fcfeb70788b7695))
- **037:** Fix socket URLProtocol hanging on response read ([`ec40ce1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ec40ce1862cff8bc4b46634c4d995694a3a998b8))
- **037:** Fix duplicate menu items in MenuBarExtra ([`10ae5d7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/10ae5d75a0a535d0e2725a1ba863747a7c2e4cc8))
- **037:** Prevent menu item duplication from SSE-driven re-renders ([`bdadabb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/bdadabb01c3991cc5d8187fcdad1dabe5ec20150))
- **037:** Replace SwiftUI MenuBarExtra with AppKit NSStatusItem+NSMenu ([`e0e9bad`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e0e9badc8fe3af9cfed49117cccb39d8d6d50c09))
- **037:** Improve tray menu UX based on MCP accessibility testing ([`168734a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/168734ac130d8b3e4ea00b3d10d3efa27fca0356))
- **037:** Fix 4 major UX issues in tray app ([`180b680`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/180b68000370524bf9c20a8c244b840cd035e0f3))
- **037:** Fix remaining UX issues + add SecretsView ([`f0f5a2e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0f5a2ec51298de74d5f60c5e808b855e8d5919f))
- **037:** Replace List with ScrollView+LazyVStack in ServersView ([`df184b9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/df184b914c5b460e0d6e26c95081292cf8328f28))
- **037:** Fix servers list, secrets API, activity filters ([`d2cff8d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d2cff8dc8094a9881e207792e8a0853c1ad96cfd))
- **037:** Use List with .id() for servers view in NavigationSplitView ([`3702662`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3702662136cde17db46bdfd4a2ac59d03ee0e449))
- **037:** Replace SwiftUI List with AppKit NSTableView for servers ([`1bfd592`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1bfd5928cdf8700bf9efaae6d1e2a524c23c34ba))
- **037:** Fix OAuth login and server action handlers ([`60d157b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/60d157b9687ee8844cc59ff7ebd171b0e900e4db))
- **037:** Fix double-v version display (vv0.22.0 → v0.22.0) ([`a7a4ac2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a7a4ac26555036f42135db2c36bb85dfdd572933))
- **037:** Remove SearchView, use MCPProxy icon for tray + window ([`4b71d0f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4b71d0f440c4d341662b03aed16088b16cba0e10))
- **037:** Fix tray icon showing red when most servers are healthy ([`15c0d22`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/15c0d225b6dafbd1dcfc375f9b1eafd2e1810f11))
- Exclude annotations from tool quarantine hash to prevent false change spam ([`91bbc18`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/91bbc18d92e9d4b42f9f643179546b2823cb8cf1))
- **037:** UI polish — fonts, icons, add server button, info button, activity detail ([`d7cdf50`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d7cdf50a648229cd20a0d0767e67aa03602342f9))
- **037:** Working pause/resume, icon overlay, status dot, tool diff ([`0d8db1b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0d8db1b090ed38761d387feb0c863bb6cc1b290c))
- **037:** Fix tray icon grayscale, add Core to pause/resume labels ([`fb11fec`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fb11fecbce82c9858d72bfb7c3d057880d290be5))
- Eliminate false tool_description_changed events via self-healing hash migration ([`be5cca4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/be5cca423a34e003b2083f1ea429221dc6be5aac))
- **038:** Improve screenshot and submenu reliability, fix docs ([`497c420`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/497c420f9e6d5be5eb29c18d22926f9976a1b1d1))
- Resolve API deadlocks, export pagination, annotation filtering ([`3df29a1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3df29a1c3d5207927f2f409f0abb91fbd4cf935e))
- **037:** Auto-refresh for all views via SSE events ([`47b7755`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/47b77556997f098b2d6d77b67cbfea84c36a8546))
- **037:** Server logs display — parse structured API response, color-coded ([`6db19de`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6db19def053c84a757960dcd5e7f2c141566e48a))
- **037:** Server disable uses correct /disable endpoint ([`2a47d92`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2a47d9238eede0c2efe40296ddb2c253bb23de55))
- **037:** Tray menu shows correct status dots for disabled servers ([`4bc5f50`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4bc5f50bb0f528d47c696c7ca219967a2fbe8eb1))
- **037:** Zoom fonts-only, core status banner, bigger tray icons, red delete ([`2c7fe27`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2c7fe2710b0a30660db6cd319fc7e6ecda44c6d1))
- **037:** Remove GeometryReader+scaleEffect that broke all page layouts ([`59cf4b0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/59cf4b072d811452370f50a3156a98a11db13875))
- **037:** Working Cmd+/Cmd- zoom via NSView.setBoundsSize ([`7407d7f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7407d7fd2f578a52d9bde5bdb590e98082eb04be))
- Use swift build + swiftc fallback instead of xcodebuild for CI ([`6c379a8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6c379a886dd3c15657c612d78500e0dcabd91b38))

### Documentation

- **037:** Add autonomous execution report ([`e71a3ab`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e71a3abb6c3527d54078932118b1c9fd7ef7febd))
- **037:** Update report with compilation verification results ([`ddb1f2c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ddb1f2cd4b1b95e6574ea6f0779de747bf7028c0))
- Add macOS tray build and UI testing instructions to CLAUDE.md ([`d001c11`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d001c116ed8e44e7126396f27d33dc089f65531c))
- **qa:** Update QA report — 27 PASS / 0 FAIL / 3 SKIP after fixes ([`28d52f4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/28d52f4740cc111ffae5f6bf563955caf0bc9b1d))
- **qa:** 100-scenario intensive QA report — 86 PASS / 14 FAIL ([`c22ed04`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c22ed047cadff9ab077357096cb1cad863604ea8))
- **qa:** Final 100-scenario report — 96 PASS / 0 FAIL / 4 SKIP ([`ee5fae6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ee5fae69bec8100d9fd65720d7f4ab622671dcdd))
- Design spec for CI Swift tray app build + installer updates ([`f0d312a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0d312a2b323f473df00fa66af388dca1e501d42))

### Features

- **037:** Add specification for native macOS Swift tray app ([`17e3f87`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/17e3f87e82130125555a8fc31b6be5a6f56a2b59))
- **037:** Add implementation plan and research artifacts ([`a86fa93`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a86fa9349b92bc5fa3310367bf4b06e46482ca64))
- **037:** Implement native macOS Swift tray app ([`5866a7b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5866a7b4814623d6b11ee707f52211db97882cff))
- **037:** Add health badge icon and notification triggers ([`24e843b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/24e843b2edc55b1d567609529fe1be11462ac898))
- **037:** Implement GitHub Releases update checker ([`4f2ae45`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4f2ae4576c569e2520346bf10277faef79b883aa))
- **037:** Implement main app window with sidebar navigation (Spec B) ([`7276a84`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7276a8475402c73dc4855e76f528b4714d04e16b))
- **037:** Apply research recommendations — accessibility IDs, menu delegate, Equatable rows, activation policy ([`f422559`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f4225592744b4f2ec08d7e4507aa32bc640b82e6))
- **037:** Improve tray menu — servers submenu, auth indicators, fix attention filter ([`1d7b1dc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1d7b1dca30aeeed43fcbfb92e1141347110c8b6d))
- **037:** Add improvement spec for tray app P1/P2 features ([`02b1686`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/02b16862a133f49b39cc9da585d602e86a6362f8))
- **037:** Implement P1/P2 improvements — server detail, add/import, search, context menu ([`d3e9c54`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d3e9c54f683d40f68231da1a12d6f089e0d98065))
- **037:** Add Dashboard, Apple-style sidebar icons, fix import spinner ([`d450494`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d450494f7fbd1714484cb63591f5311acdc0fcbe))
- **037:** Docker Desktop-style tray icon, pause/resume, simplified menu, tool diff ([`cb46aa5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cb46aa59f466bb689e50904728f181d5a012a3e1))
- **037:** Activity Log — SSE live updates, colored JSON, intent, export ([`3eb922d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3eb922da2a34cf4bf2448f5f7824113a1e2d27a4))
- **038:** Add specification for MCP accessibility testing server ([`62ea4b1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/62ea4b1881977853ee68ef13c4ca31bf83adf6c7))
- **038:** Implement MCP accessibility testing server ([`e7a6229`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e7a622964429b5c5c524f148b7c7a3c7c50a206d))
- **038:** Add screenshot capabilities to mcpproxy-ui-test ([`c6f69f0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c6f69f00f6a5152dc5b8daa004606fa1d824e339))
- **037:** Docker Desktop-style server table, improved tabs and logs ([`ae4e2f3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ae4e2f3af76076322fdbb135e92a5618cef9aa74))
- **037:** Expandable tool details + sortable server columns ([`10b3239`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/10b323915a5d89e2acb1a6c8e751992f2903d6f6))
- **037:** Dashboard matches web UI, Activity Log as table ([`6a062c3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6a062c38291458546f974e0da7e037127ae3da6c))
- **037:** Cmd+/Cmd-/Cmd+0 zoom for main window, View menu ([`222feab`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/222feab753f05aa5f4184912f206ca363dd7986c))
- **037:** Fix zoom, Stop/Start terminology, server action labels ([`050192b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/050192b428a3a612ea00115baa50ff9a0705caa6))
- **037:** Font-only zoom via custom EnvironmentKey — panels stay fixed ([`e0bcf1d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e0bcf1d758cf950f5b1ce0aba78ae8acdfd78ae1))
- **040:** Add/Edit Server UX improvements for macOS tray app ([#359](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/359)) ([`82718be`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/82718be1bf6073fca42a2c0165adbae91602297c))
- Build Swift tray app in CI, use in macOS installers ([`8c72179`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8c72179f5037def81a9ed337599d52d9b0ef0590))

### Refactor

- **037:** Apply macOS HIG design guide across all views ([`b11b527`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b11b5275dd545b093f8494ac4c51ec567c0adbd8))

## [0.22.0] - 2026-03-23

### Bug Fixes

- **test:** Increase socket E2E server startup timeout (#352) ([#352](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/352)) ([`b48acb9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b48acb9514a2fc405794f7bbf1a829ec53bb9c3b))

### Features

- Enhanced Tool Annotations Intelligence (Spec 035) (#342) ([#342](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/342)) ([`a0cdc37`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a0cdc37716e79db49913d634f944494b4965ebb3))
- Anonymous telemetry and in-app feedback (Spec 036) (#345) ([#345](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/345)) ([`14c1cc9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/14c1cc9d3e5f6ab3d66beb37a60ea8f0a48e0c08))

## [0.21.4] - 2026-03-23

### Bug Fixes

- Session tracking not updating in Recent Sessions (#344) ([`a015c3c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a015c3c5cd0cd1ce8da4d3432b30f47795ba7dc7))

## [0.21.3] - 2026-03-20

### Bug Fixes

- CopyServerConfig missing SkipQuarantine and Shared fields ([`0fab80a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0fab80a2571e62c1047d280ea33b6dc8530daaf2))
- Adapt retrieve_tools instructions for code execution routing mode ([`2a29c40`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2a29c40b4e323eec7618de3db073f0fb12935624))
- Escape Windows backslashes in TestLoadConfig_DataDirExpandFailure ([`8c59657`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8c596571623ef560238aebec8bfe4c92dc76b44a))
- Handle unresolved secret refs in data_dir on Windows ([`1b8e235`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1b8e235dfb07d55a7194ef22d2d9936e54c79cfc))
- Include annotations in tool quarantine hash with backward compatibility ([`fec0448`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fec0448f16f7a8e44922b142dead2abdb26aa4e7))
- Avoid unnecessary DB writes on every approved tool check ([`984b237`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/984b23746b199e9c95756f6fb9b2005f38bf627f))
- **ci:** Upgrade macOS runners from macos-14 to macos-15 ([`d5b3d30`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d5b3d306e19a388259c278b6b476c4b89b3e62be))

## [0.21.2] - 2026-03-11

### Documentation

- **spec:** Add spec, plan, and tasks for expand-secret-refs ([`82548d7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/82548d7e1e3387ac9dcf1ac5196687eeeb0e0b51))

### Features

- **secret:** Add ExpandStructSecretsCollectErrors and export CopyServerConfig ([`0bb5a09`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0bb5a09f9824f4b1e7ff5f3888f5be817b04e125))
- **secret:** Expand env/keyring refs in all ServerConfig and DataDir fields ([`78bdf57`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/78bdf574cbf9533d9cbe1b3f7bb618576b3147d4))

## [0.21.1] - 2026-03-11

### Bug Fixes

- Improve code_execution tool description for AI agent reliability ([`1226bf9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1226bf9d5251d1e1f3d44b79a697c00c8e2b8679))

## [0.21.0] - 2026-03-11

### Bug Fixes

- **teams:** Wire up all teams API endpoints and fix frontend data mapping bugs ([`be2a5f7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/be2a5f7b487a2028055f728cc46241682a3d0201))
- Make swagger output deterministic by ignoring teams-only config ([`efbca20`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/efbca20ecfd1d464fd302fe00494c09b8f880548))
- Add nil check for config in routing mode handlers ([`2045c05`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2045c0558fef164faaf876ac23311c9257f838e7))
- Address code review issues C1, C2, I1 ([`08198db`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/08198db35372ebb134d9040edff289e1333e23e3))
- Use switch statement for routing mode in doctor command ([`7e60915`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7e60915b74794f572765292a0471cb9e765ed269))
- Add SkipQuarantine to field coverage test exclusion list ([`f7a7de2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f7a7de271946d3da944c6e28b8df32f35a1cf9d6))
- Clean up tool approval records when server is removed ([`9c3e554`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9c3e55491caefc3563961523c27582a637b0c7ab))
- Resolve lint and E2E test failures in tool quarantine PR ([`f00c04a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f00c04a4fe7a7b3383cc083a0313e3533eca5be3))
- Auto-approve tools for trusted servers and add quarantine stats to servers API ([`0293953`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/02939538a46ca3961b177c0831e32b201df95b83))
- Reduce tray log noise by changing server state polling to debug level ([`4238532`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4238532185cea223e4c15711f1215f1a23abb54c))
- Auto-approve tools for non-quarantined servers on upgrade ([`3407971`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3407971061b853887e9bc11046470db9961e1321))
- MCP endpoints dropdown not opening due to overflow-x-hidden clipping ([`d44365a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d44365aa0341a52a26da5d83bd912451422d7bbe))
- Simplify code_execution description and add it to /mcp/call mode ([`673f30e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/673f30ec3818ea3eae359b7209fe4d17fe59db00))
- Repair failing tests and broken docs link ([`8e6ac56`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8e6ac56966effdaa7eadac34802ffb8b7d4cf41c))
- Remove broken Docusaurus link to internal code_execution docs ([`6b64e0b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6b64e0bddc96cb4642b83131e459b3381b749a8b))
- Refresh server data after tool approval to prevent stale quarantine counts ([`11ed0df`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/11ed0df82ca12b38a4c6ed9464f1c650973058f7))
- Fetch tool diffs for changed tools to enable visual diff in Web UI ([`177df07`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/177df072964e91ddd3aad7b9b4a017f2243f7457))

### Documentation

- Update CLAUDE.md with tool-level quarantine documentation ([`aa19713`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/aa19713d4886089ab625dd85a7e24aa95aecd191))
- Add documentation for routing modes, quarantine, TypeScript, and modern JS ([`f3f7520`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f3f7520d3acb1d3c42dbf7726e9d797d55a28201))
- Add routing modes, tool quarantine docs and QA report ([`f204be8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f204be8c42648b20b7b1eeee8d55836f239c2c9f))
- Add quarantine QA testing design spec ([`3352889`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/335288904e48f13d0a664e84c5be685a89dc15bc))
- Add quarantine QA testing implementation plan ([`43f49a1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/43f49a15bb5302d61196d9e595921fc00217f201))

### Features

- **teams:** Shared server toggle, per-user preferences, and activity log isolation ([`a33ce88`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a33ce88cd387dbcca88a89953fc5e6f0b8c5e1f5))
- **teams:** OAuth auth, multiuser routing, workspace isolation, and activity enrichment ([`c458e1a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c458e1abc2ebab043ee5f56421325dcf8b8d7c80))
- **teams:** Admin server management, per-user agent tokens, and UX redesign ([`56021ee`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/56021eeb77542695ef7ceae87ff0eea9ae22164b))
- Add TypeScript language support to code_execution tool ([`72bfb05`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/72bfb05471a4c21cbb4dc97fe54d060e78d090ab))
- Add routing_mode config field with validation (Spec 031) ([`1875d86`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1875d865b1697e105031d7d098f4b75f38801547))
- Add routing mode logic for direct and code_execution modes (Spec 031) ([`066442b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/066442b45eaaaa3ce6003b379eb5ca4fba0dfee1))
- Add multiple MCP server instances for routing modes (Spec 031) ([`7e2c1e1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7e2c1e15129a6612d77d1b76d6692264bef8d1af))
- Register dedicated HTTP endpoints for routing modes (Spec 031) ([`6eb6f5d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6eb6f5d71654def010c00624c33adbe86f8a4013))
- Add auth context enforcement to JS runtime call_tool (Spec 031) ([`d2b603d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d2b603dc045de62742ab06ef23be3d7fca7dab5c))
- Add routing mode to status and doctor CLI output (Spec 031) ([`c40c818`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c40c818327a7ae3753c6261bd3abb633216e0812))
- Add routing mode API endpoint and status field (Spec 031) ([`f12138b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f12138b131646f24b1000e448cce2c0839484a47))
- Add routing mode display to Web UI (Spec 031) ([`918fdf6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/918fdf6981b4899e4225727066fb880d326caaa5))
- Add ToolApprovalRecord model and BBolt storage ([`49aa2bf`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/49aa2bf08e291628dd2520654454565eaa281d95))
- Add quarantine_enabled and skip_quarantine config params ([`d71c8a0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d71c8a07b7a19e6d56b94cf329592f8261abc72c))
- Implement tool-level hash checking and approval logic ([`680ce0e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/680ce0e3c7a130172b36b13288282b895043f68d))
- Block tool execution for unapproved/changed tools ([`5a78e9d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5a78e9dddaef5862c98c14fa36c65838d5e4726a))
- Add activity logging for tool quarantine events ([`3a225b7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3a225b72429bb5342b1ddf3ed7682e0fdab2b60c))
- Add tool inspection and approval REST API endpoints ([`5483ffb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5483ffbc28013040e76954b8744a3c3da002f3a4))
- Add CLI inspect and approve commands for tool quarantine ([`1aaadf9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1aaadf994014611ec5a8463c64c8582ee36ae569))
- Add tool quarantine UI in server detail view ([`b35e6ad`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b35e6ade5e7233821d495237b96fdca25c42b83f))
- Extend quarantine_security MCP tool with tool-level operations ([`7fc5566`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7fc5566b1979597f211be69ad2b4843c68e7b572))
- **frontend:** Show quarantine tool count on servers list page ([`f1d4d52`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f1d4d525404c6b023cb3eaaddbd262a02f0b7bd1))
- Add quarantine tool visibility to Dashboard and CLI doctor ([`5647518`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/56475186da9659751b67b80740302750c20cfd21))
- Give each routing endpoint its own focused tool set ([`add42aa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/add42aa9dcd110b8a8d1cd4df46f136f98cb773a))
- MCP endpoints dropdown, CLI endpoints, version, management tools ([`218de1f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/218de1f74396f326b5db24ec4ad5ad1f56e52def))

### Refactor

- Remove tool_search_tool_bm25_20251119 internal tool ([`4753adf`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4753adfbb530960679f2c03225bf14228233ba95))

### merge

- Resolve conflicts with main branch ([`d0435cc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d0435cc5a377d143a6516c4fcb357e32cbf3a5a3))

### rename

- Teams -> Server across product naming, build tags, and UI ([`32f688c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/32f688cb0087b3c1f51785af3d611b49056f9789))

## [0.20.2] - 2026-03-09

### Bug Fixes

- Prevent _auth_ metadata from leaking to upstream MCP servers (#322) ([`542e7de`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/542e7de5f952371302d8225372a6093cf6886fa4))

### Features

- **spec:** Add MCPProxy Teams specification (029) ([`72ed1ae`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/72ed1ae68083982f3e093f3b78d2b0fda6bd2097))
- **teams:** Repo restructure for personal + teams dual-edition architecture [#029] ([`f5a399c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f5a399ccda0e34ec2b75d32f632348173cfbe5b6))

## [0.20.1] - 2026-03-08

### Bug Fixes

- Panic recovery returns proper error result; safe tokenizer nil handling [#318] (#320) ([#320](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/320)) ([`83508f5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/83508f54557495bfb86ee00207a3976053227713))

## [0.20.0] - 2026-03-06

### Features

- **auth:** Scoped agent tokens for autonomous AI agents [#028] (#319) ([#319](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/319)) ([`522a9e9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/522a9e9edd4d9f7cfab8fd4ccfef7619bbfef566))

## [0.19.1] - 2026-03-03

### Bug Fixes

- **oauth:** Normalize HTTP 201 token responses for Supabase compatibility (#317) ([#317](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/317)) ([`ed10f89`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ed10f8981a34113436b9f1776b3f6462a4715744))

## [0.19.0] - 2026-03-03

### Features

- **cli:** Add status command with API key display and web URL (#316) ([#316](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/316)) ([`30cb722`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/30cb722a16975678c7967db865ddddd6cad86583))

## [0.18.1] - 2026-03-01

### Bug Fixes

- **oauth:** Prevent refresh manager deadloop on server-not-found errors [#310] (#315) ([#315](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/315)) ([`fac401e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fac401e60e7ff1ae8a0bd6b928748685106869af))

## [0.18.0] - 2026-02-28

### Features

- **tui:** Terminal UI dashboard [#300] (#301) ([#301](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/301)) ([`55885b7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/55885b733608b30d8c19196350d295cd9ca0347a))

## [0.17.8] - 2026-02-28

### Bug Fixes

- Correct quarantine API docs and wire tray unquarantine menu (#312) ([#312](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/312)) ([`3640c3f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3640c3f51965cc97baf6041c9b827e81fbddd41a))

### Documentation

- Add comprehensive keyring integration documentation (#314) ([#314](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/314)) ([`591f861`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/591f861953172af5aedcad0e005a9c55f959689a))

### Features

- **oauth:** Add Runlayer mode to Go OAuth test server ([`7d71cb9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7d71cb97e50e05dc03a7c9170bb023f2413cfcb9))
- Modifying CLAUDE.md (Constraint Architecture for Go) ([`a3ed8b6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a3ed8b6cae96a925557c722016c7d952107f92a0))
- Add --no-quarantine flag to upstream import and fix import docs (#313) ([#313](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/313)) ([`a6611f2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a6611f2347be455d897026ccd036bc04656eca5d))

## [0.17.5] - 2026-02-15

### Documentation

- Add Google Antigravity setup guide to client configuration ([`1323928`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/13239282fe7fc4d84b1bbb6ad6ed1197328fc471))

## [0.17.4] - 2026-02-07

### Bug Fixes

- **oauth:** Prevent orphan cleanup from deleting valid legacy tokens ([`8383d04`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8383d043fb198976cde33697986bfe2908a85745))
- **oauth:** Preserve DCR credentials when mcp-go saves tokens ([`f7258a3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f7258a3d2b0e72414e549c458e70539c1cd42141))
- **oauth:** Resolve stale stateview and handle server 5xx as token error ([`5485a4b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5485a4b92a3c0ab12662ad4d20938b84ca59bb33))
- **supervisor:** Resolve stale "Connecting..." status by fixing lock ordering in reconcile ([`f2d2919`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f2d2919a30fdf4a5adaa4931d7b5b37571633b4b))
- **oauth:** Prevent Connect() from blocking Disconnect and HTTP startup ([`d19f142`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d19f1429a8fd883eec64f065229c5e4851e4fc2a))
- **oauth:** Resolve deadlock in persistDCRCredentials during Connect ([`41e272f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/41e272fc6e35215b59b095298fc9ebf6d9cb0dbf))

## [0.17.3] - 2026-02-07

### Bug Fixes

- **oauth:** Implement direct token refresh bypassing ForceReconnect early return ([`432a880`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/432a880e3a25ddcd3e91f057b1ae2e812c075864))
- **oauth:** Persist DCR credentials for proactive token refresh ([`822dd99`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/822dd99d8edd0c8168f38a83e90b487a09bcba92))
- **oauth:** Force mode for standalone auth login, expand error detection ([`b04c44b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b04c44bb653f9bc86fa8ff17f7efa8642a12ff48))
- **oauth:** Guard nil storage and handle missing expires_in ([`2fab66b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2fab66b86eefa0a1594b85358a4ade9c4b8c4148))
- **oauth:** Harden token refresh with timeout and endpoint validation ([`193b22a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/193b22a78d0bc567ec84883895fc9707cee2b09a))

### CI/Build

- **signing:** Switch Windows installer to production SignPath certificate ([`c963d17`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c963d177ca47b57dacfa9dcddc00c7c2c125e716))

## [0.17.2] - 2026-02-03

### Bug Fixes

- **sse:** Emit servers.changed event after tool discovery completes (#292) ([#292](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/292)) ([`29cf86e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/29cf86eddc9d95ba447bca53dc82947180f5d263))

### Documentation

- Update demo video link in README ([`fa9ecf1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fa9ecf12be391b8884ea6808cd4ddec1a9760378))

### Refactor

- Split connection.go into focused, maintainable files (#291) ([#291](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/291)) ([`aabcbc9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/aabcbc9ccef16983d4ce19dd34481164008b6e13))

## [0.17.1] - 2026-02-01

### Bug Fixes

- **ui:** Use theme-aware colors in sensitive data detection panel (#290) ([#290](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/290)) ([`631e93c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/631e93c94642f46febd61c52067ee251b392b688))

## [0.17.0] - 2026-02-01

### Features

- **security:** Add sensitive data detection for tool calls (Spec 026) (#289) ([#289](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/289)) ([`dfe3cbb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dfe3cbbbe6c240198fb62ba8099dcebdfea53043))

## [0.16.8] - 2026-01-31

### Bug Fixes

- **upstream:** Clear stale core client state before reconnect (#286) ([#286](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/286)) ([`47cc680`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/47cc68062992b69d1a94fc449c97bb9e96d0d776))
- **build:** Use correct module path in ldflags for version injection (#287) ([#287](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/287)) ([`31ee931`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/31ee931c8669c13457e0e5ab5473ff9ad89cd93d))
- **oauth:** Clean up orphaned tokens on server removal and startup (#288) ([#288](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/288)) ([`55b0861`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/55b08614f6e37ff4797a16681778618d2b75aafc))

## [0.16.7] - 2026-01-31

### Bug Fixes

- **oauth:** Handle rate limiting in resource auto-detection ([`53b7141`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/53b714117b0aad1bf879fcca41ee97a20914710d))
- **lint:** Use fmt.Fprintf instead of Write([]byte(fmt.Sprintf(...))) ([`a149e30`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a149e30f71cd235465029707f7faf3a7c731cc61))
- **oauth:** Limit response body read size in resource detection ([`c027a0a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c027a0a1b594bee29ebeb94e0bead456cb549847))

### Features

- **test:** Add rate limit testing support to OAuth test server ([`f11d63c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f11d63ca31f4d1da7ad9a76069437a711a9ceb83))
- **oauth:** Add context support to rate limit retry logic ([`2a0bb15`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2a0bb15fec978cffc1cbf674b3aee61eec2f4a55))

## [0.16.6] - 2026-01-29

### Bug Fixes

- Resolve secret references in HTTP headers (#284) ([#284](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/284)) ([`05fc9b1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/05fc9b13099e46fbbfca1fac9379e72a971eeeb9))
- **index:** Clean stale tool entries and include description in hash (#283) ([#283](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/283)) ([`cfe9276`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cfe9276f62877ed4083f8d8f447e2458d2b245d7))

## [0.16.5] - 2026-01-27

### Bug Fixes

- **oauth:** Drain stale callback params to prevent state mismatch on retry (#281) ([#281](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/281)) ([`f63c3f8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f63c3f81c2f91043bf26fad927ca83837c98bdc3))
- **intent:** Infer operation_type from tool variant instead of requiring it (#282) ([#282](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/282)) ([`dd9376a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dd9376afc7db5d6778eb1db9fb72be2536994dbc))

## [0.16.4] - 2026-01-23

### Bug Fixes

- **ci:** Handle SignPath output with correct filename ([`6b13013`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6b1301390f070af7f905ac6121afc50807017fd0))

## [0.16.2] - 2026-01-23

### Bug Fixes

- **ci:** Upload exe directly for SignPath signing ([`257c592`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/257c5923ec3fb713c27dab6f93ddf4e94ef6cdf9))

## [0.16.1] - 2026-01-23

### Features

- **ci:** Add SignPath Windows code signing to release workflow ([`f8c98f8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f8c98f84f123fcd1ac021206bed5ca93d8c74d6d))

## [0.16.0] - 2026-01-23

### Features

- **oauth:** Implement token refresh reliability with exponential backoff (#023) (#255) ([#255](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/255)) ([`9fee199`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9fee199de36d802e833ac6c3acee477ab6b2c5e3))

## [0.15.5] - 2026-01-22

### Bug Fixes

- Add JSON/YAML output support to auth status command (#270) ([#270](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/270)) ([`1da0980`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1da098042377ae291c2f45fad9b60bd6f63f0c45))
- **oauth:** Inject resource parameter into auth URL for API/UI login (#271) (#272) ([#272](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/272)) ([`6a9b793`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6a9b793965edb3049cf950aaefc5a2cd28048ead))

## [0.15.4] - 2026-01-19

### Bug Fixes

- Add hostArchitectures to PKG Distribution.xml to prevent Rosetta prompt (#268) ([#268](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/268)) ([`b640020`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b640020621eb569453abc525a221745c4d2e7493))

### Features

- **auth:** Add --all flag to auth login command (#267) ([#267](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/267)) ([`d30d95f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d30d95f6d67dbbf5733bb7943c88c28fcf178faf))

## [0.15.3] - 2026-01-19

### Bug Fixes

- Use consistent server count across UI components (#266) ([#266](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/266)) ([`4fb4329`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4fb4329b15c75c886bbc06e6c1c0423bdb8edaaf))

## [0.15.2] - 2026-01-19

### Bug Fixes

- Use correct API for canonical path imports in web UI (#265) ([#265](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/265)) ([`da6e817`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/da6e817bf06990016bcdc66c9a57c58a2c156759))

## [0.15.1] - 2026-01-18

### Features

- Add quick import hints for canonical config paths (#264) ([#264](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/264)) ([`f2af455`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f2af455b72aad3e06979ca1e5e383e3aff67a2e1))

## [0.15.0] - 2026-01-18

### Bug Fixes

- Update Homebrew workflow to use pre-built binaries (#257) ([`888b937`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/888b9372546c12c5a4717d0bc435263903565417))
- Implement RFC 8414 compliant OAuth metadata discovery (#262) ([#262](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/262)) ([`028e799`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/028e7992c479a557fa91cfa52ecbe1bcdf4533c4))
- Correct YAML heredoc indentation in release workflow ([`8aab666`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8aab666c46abcea0f3ebb60312b71b255a5a7584))

### Features

- Add config import from Claude Desktop, Claude Code, Cursor, Codex, Gemini (Spec 025) (#261) ([#261](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/261)) ([`aa4419e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/aa4419eb9e66f8cf0e627b0a4800b356e846d358))

## [0.14.0] - 2026-01-14

### Bug Fixes

- Remove unused isolation_* parameters from upstream_servers tool (#252) ([#252](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/252)) ([`1b18c07`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1b18c07d6d4848c88d0c35fc53a43d4be17be9b9))
- Improve tool descriptions and error messages for AI reliability (#258) ([#258](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/258)) ([`2c1b3ae`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2c1b3aedb1d69977460c38bd38e33dd46e3785b1))
- Use canonical module path for go install support (#259) ([#259](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/259)) ([`50e5bb8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/50e5bb87726c5e7b015a0f39c90300e92487416a))

### Documentation

- **spec:** OAuth token refresh reliability (#023) (#253) ([#253](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/253)) ([`91b6f59`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/91b6f59f47f06f81e97babb0b847d9c033739b0e))

### Features

- **activity:** Expand activity log with new event types (Spec 024) (#254) ([#254](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/254)) ([`29fcf5f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/29fcf5fe1909cf4499ef4c8c939ec7d7bdd914f0))

## [0.13.1] - 2026-01-11

### Features

- Add 410 Gone error handling for deprecated MCP endpoints (#248) ([#248](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/248)) ([`83e2575`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/83e25757f69a558da30c83222a307705c0874177))
- Implement smart config patching for upstream servers (Spec 023) (#249) ([#249](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/249)) ([`c903d3c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c903d3c480b1d834c0b64e9829e1630d2dbd19b2))
- Implement RFC 7396 null-means-remove for env/headers patch (#250) ([#250](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/250)) ([`4f38376`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4f38376770c9a57bdacb5df55ae112f995a9730c))

## [0.13.0] - 2026-01-10

### Bug Fixes

- **oauth:** Open browser window for 'auth login' command (#155) (#241) ([#241](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/241)) ([`b32ea51`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b32ea51d9be3d1191f1bdaf5929290c9f97a94ed))
- Remove timestamp from generated contracts.ts to prevent churn (#246) ([#246](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/246)) ([`1dc5323`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1dc5323022a531fde4786c36ab626eefd2d2d18b))
- **frontend:** Make server name clickable in attention banner (#244) ([#244](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/244)) ([`86cca77`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/86cca77e22f56a79ef951aaccba09a9b8c725954))

### Features

- **api:** Add request ID tracking for end-to-end tracing (#021) (#237) ([#237](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/237)) ([`b6e3253`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b6e325359313fbe40bec319520bd42a657693dd1))
- Structured Server State - Health as Single Source of Truth (#205) ([#205](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/205)) ([`2841f33`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2841f33e9a5311b6ce471ce84c64dd00cb27d913))
- **oauth:** Persist callback port for redirect URI consistency (#022) (#247) ([#247](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/247)) ([`767bd78`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/767bd78188aa79cc62bfa76c39dff5d3b08889e6))
- **oauth:** Add structured error feedback for OAuth login (Spec 020) (#243) ([#243](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/243)) ([`80f2226`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/80f2226e7f60af5c85473bfafdee8c1c154a7ba1))

## [0.12.0] - 2026-01-07

### Bug Fixes

- Exclude quarantined servers from tool discovery and search (#219) ([#219](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/219)) ([`48199ef`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/48199effb431d361c1e1a71ae25c7508a5cf526e))
- Resolve activity CLI bugs from QA report (#223) ([#223](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/223)) ([`87cc79c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/87cc79c3f8e3d4b5966e70f04c1609ed6c2446d5))
- Log CLI tool calls with source indicator (#224) ([#224](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/224)) ([`9bca687`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9bca68766e561e74a2baa64af66f46b2747490ae))
- Address QA report issues for intent declaration (#226) ([#226](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/226)) ([`abee7d8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/abee7d83d9edb9ed5a78941fce48d014422b7627))
- Search-servers command now outputs table by default (#228) ([#228](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/228)) ([`cf627d8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cf627d8e4a023855016a38827a294e0d06e86fb9))
- Log invalid intent attempts to activity log (#232) ([#232](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/232)) ([`e8ca63a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e8ca63a85fb60b2a0a0e4a3d8e4ec62dece84f1b))
- **cli:** Correct field names in activity watch display (#235) ([#235](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/235)) ([`730d5e3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/730d5e38ef16ac300686c80d5496294bcdb89312))
- **oauth:** Detect empty client_id when DCR fails with 403 (#236) ([#236](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/236)) ([`4c4d070`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4c4d070b9356c5e3761e4e00a1ed56118295b0d5))

### Documentation

- Add RFC-003 Activity Log & Observability ([`d6c143d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d6c143d1a3198a23d9ba728fe67bba7f0ec2ee55))
- Simplify GitHub release page for better UX ([`b6ebddc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b6ebddcc9b67be14a63db5cb6dc0d11c1c3f2513))
- Fix call tool flag documentation (--json_args not --input) (#227) ([#227](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/227)) ([`80efea3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/80efea3101a1c9691c5a59713efc9bfddeaa12fa))
- Fix CLI flag references in spec 018 (--json_args not --args) (#229) ([#229](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/229)) ([`e53a07a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e53a07aab720f85bb62e8c125ca3e5bf38966487))
- Add --intent-type filter and INTENT column to activity docs (#230) ([#230](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/230)) ([`4cfb008`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4cfb008a05f5032df808aefecd732530826cbded))

### Features

- Implement CLI output formatting system (Spec 014) (#216) ([#216](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/216)) ([`c1dd8eb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c1dd8eb0621377e9b4a8c332855d5a0769d2fe5d))
- Add CLI commands for server management (Spec 015) (#217) ([#217](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/217)) ([`4070760`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/407076034ab38daea63959123244bff4f6d60ce0))
- Implement Activity Log Backend (Spec 016) (#220) ([#220](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/220)) ([`4c42097`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4c420973498c4c8b47a0c5acf89878bc9376e146))
- Implement Activity CLI Commands (Spec 017) (#222) ([#222](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/222)) ([`0a6c614`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0a6c61479c95f97f136235f822b5324208200e1f))
- Implement Intent Declaration with Tool Split (Spec 018) (#225) ([#225](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/225)) ([`5cbdb63`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5cbdb63db133bcd58af8fcde13ac9d6b4d9ef3fd))
- Add --no-icons flag for activity list and show commands (#231) ([#231](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/231)) ([`09cf95d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/09cf95d48274f7fd53f9350eef9ff53a482db4fd))
- **frontend:** Add Activity Log web UI with real-time updates (#233) ([#233](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/233)) ([`635bbc0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/635bbc032e3d8889fa9ae36306fa025b8d352b13))

## [0.11.5] - 2025-12-21

### Bug Fixes

- Increase subprocess shutdown timeout from 2s to 10s (#214) ([#214](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/214)) ([`f94cf5c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f94cf5cecf4a856972b87773fe8bcc4b96d36823))

## [0.11.4] - 2025-12-20

### Bug Fixes

- Add refresh option to update check for doctor command (#213) ([#213](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/213)) ([`4272aa3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4272aa36f46283604e95d8ea4bd90f462cee6ac8))

## [0.11.3] - 2025-12-20

### Features

- Subscribe to notifications/tools/list_changed for automatic tool re-indexing (#212) ([#212](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/212)) ([`575dcaa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/575dcaac671355e0ae9542fc5c5b97f1ac3c3775))

## [0.11.2] - 2025-12-20

### Bug Fixes

- Set httpapi.buildVersion via ldflags for correct version display (#211) ([#211](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/211)) ([`e4cd880`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e4cd88015c902ec26259be134bd455af8cc71b68))

## [0.11.1] - 2025-12-19

### Features

- Add tool cache invalidation with differential update logic and manual discovery trigger (#208) ([#208](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/208)) ([`080156d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/080156dbd7aeeb215146d6a03d68c49e67c417e4))

## [0.11.0] - 2025-12-15

### Bug Fixes

- Correct Docusaurus edit URL path (#200) ([#200](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/200)) ([`c7079d6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c7079d68ebe3a039c34cfd08caaf5365dd300a43))

### Documentation

- Add introduction page with architecture diagram (#203) ([#203](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/203)) ([`0500460`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/05004608658cc9abf8cf2f806c3b4bec0f70be42))
- Update CLI and REST API docs for unified health status (#204) ([#204](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/204)) ([`c4e6545`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c4e654599c4063665c32e9eb3a9b4895dac8f253))

### Features

- Add centralized version display and update notifications (#201) ([#201](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/201)) ([`27555c4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/27555c42aec6fdf1e48bff8bd410ee102976640e))
- **health:** Unified Health Status Implementation (#192) ([#192](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/192)) ([`2173d65`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2173d656e53a0424c9f96ccfd5773a672e4670e3))

## [0.10.13] - 2025-12-14

### Bug Fixes

- Resolve path separator issues in Git Bash on Windows ([#195](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/195)) ([`e5f4ae3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e5f4ae3d40ea8e71450beaa0f31e3fbac1e5f0f3))
- Add favicon and apple-touch-icon for docs site ([`ba74ce4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ba74ce4a5e7f22c3dc1685ba26832fb5b4f5b8b1))

### Documentation

- Optimize CLAUDE.md by moving detailed sections to dedicated docs (#190) ([#190](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/190)) ([`5bbab84`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5bbab841ebdf331835ab71f7d64fe54adee1d556))
- Add Docusaurus documentation site for docs.mcpproxy.app (#197) ([#197](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/197)) ([`380057e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/380057ebb47b6b7e3143188ab0bc742a70e51e67))
- Fix incorrect config options in security and OAuth documentation (#198) ([#198](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/198)) ([`dcf9057`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dcf9057e67b74ac23adb417bb128bb9685264be2))
- Sync CLI documentation with source code (#199) ([#199](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/199)) ([`da4aeb7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/da4aeb7318bbe739513bbac0f7f0008e878b7424))

### Features

- Enable MCP prompts capability with workflow prompts (#194) ([#194](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/194)) ([`da81f59`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/da81f596ae7b1aa169313beaa5e3c2d41546158d))

## [0.10.12] - 2025-12-11

### Bug Fixes

- Add /api/v1 prefix to Swagger annotations and fix E2E tests (#186) ([#186](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/186)) ([`f0ca376`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0ca376b371ecee98388e306049b523ae956f375))

### Features

- Auto-detect RFC 8707 resource parameter for OAuth flows (#188) ([#188](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/188)) ([`6ccc07c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6ccc07c31e6c796ed3fb554baf27e560edc42c21))

## [0.10.11] - 2025-12-09

### Bug Fixes

- Enable env_json, args_json, headers_json updates in patch/update operations (#185) ([#185](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/185)) ([`b285164`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b285164cf8ac7a860e55acf78d2e91a832e80014))

## [0.10.10] - 2025-12-08

### Bug Fixes

- Improve HTTP server UI - show Login instead of Restart, use Reconnect in Actions (#184) ([#184](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/184)) ([`5df6310`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5df6310202fee5dd24c18ab94230f1666b7d420e))

## [0.10.9] - 2025-12-08

### Bug Fixes

- Resolve race condition in TestE2E_TrayToCore_UnixSocket with race detector ([`03d4607`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/03d4607a0840d0dd64d8541dcdb33b9b1c5155b4))
- Use bash shell for security test check on Windows ([`4d7e96e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4d7e96e4cfa5c57748243c2f25ea5f626123fa69))
- Add API key authentication to quarantine config test HTTP requests ([`24acc6f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/24acc6f7f758d9c240c0d2145e19541aa25baa73))
- Resolve PR artifact comment permission errors for fork PRs (#163) ([#163](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/163)) ([`c3e31d1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c3e31d13cf50527c1c33def4cf64900e0b6b578e))
- Complete OAuth config extraction in API responses (fixes #155) (#164) ([#164](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/164)) ([`9708bbc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9708bbcdf0b189f58fab3b5b116ddcd9a2887e10))
- Escape hash symbols in PR artifacts workflow YAML ([`d9cbf18`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d9cbf18d171fb5ab19175394421420fb907b4f32))
- Use heredoc with placeholders for PR artifacts comment ([`ed12f71`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ed12f7195b1f20e0fe202521f553dbc5b41a076e))
- OAuth token refresh and flow coordination improvements (#170) ([#170](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/170)) ([`260bd2c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/260bd2c675949cdeb7de295d6b26db752cd2e9cc))
- Persist DCR credentials for OAuth token refresh (#176) ([#176](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/176)) ([`e276b41`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e276b419da14ba19593593c02f0bc72aa9530f93))
- Clear OAuth error on successful reconnection (#177) ([#177](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/177)) ([`b828811`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b828811240991d2ae25f8cff88a3c4e6adaf01c0))
- Make Login button consistent with other server action buttons (#178) ([#178](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/178)) ([`58225d9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/58225d9175c514347983a53d805e95dd6bcc805d))
- Handle grace period for short-lived OAuth tokens (#179) ([#179](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/179)) ([`4c5b565`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4c5b56569ae18005bc715aed0cd84aed0a7d0d17))
- Skip browser OAuth flow when valid token exists in storage (#181) ([#181](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/181)) ([`5678086`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5678086250987fd747974b435e29a11365fd8c1c))

### Documentation

- Add comprehensive configuration reference documentation (#169) ([#169](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/169)) ([`20930b7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/20930b7dd8c9bf95608b8283746cfbece4231fee))

### Features

- Add OAuth E2E testing with Playwright (#168) ([#168](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/168)) ([`1c59b18`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1c59b18915c5ffa15e9217a20a38b1d67b10124a))
- OAuth extra params support and token status improvements (#167) ([#167](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/167)) ([`03ea346`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/03ea346fb1d1090a3bccf848a3943b9d109f1ba0))
- OAuth extra params with zero-config OAuth features (#173) ([#173](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/173)) ([`38d15dc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/38d15dc1b956922c0ed521cbc960f8568f5f1ec0))
- Proactive OAuth token refresh and logout commands (#180) ([#180](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/180)) ([`47d87b3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/47d87b3dad1fe98c6dbfdca419f0ec1a592702da))
- AI-powered release notes generation with Claude API (#183) ([#183](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/183)) ([`88c6ed4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/88c6ed4d8dc75c0579af76d7f4f614959dbcd95e))

## [0.10.8] - 2025-11-29

### Bug Fixes

- Enforce API key authentication and fix Swagger path duplication (#161) ([#161](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/161)) ([`32a0dbe`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/32a0dbe765cf6d697818c71058d7587d5ecabcbd))

### Documentation

- Add OpenAPI coverage analysis and automated verification (#162) ([#162](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/162)) ([`a9c7ba2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a9c7ba2f84a11bdfc92db09ef4334f928e034882))

## [0.10.6] - 2025-11-28

### Bug Fixes

- Skip TestE2E_TrayToCore_UnixSocket on Go 1.23.x ([`066bff4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/066bff4223571b47bbaf1569da9c008ab8c871cd))

### Features

- Add Unix socket support to tools list, auth login, and auth status CLI commands (#152) ([#152](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/152)) ([`b5bba51`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b5bba514f1b3f3b5e9c4b126acc56d6e34479960))
- Refactor REST endpoints to use management service layer (#153) ([#153](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/153)) ([`fc94510`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fc945106498ff8536d4cdfdd4ee9d44f22f51c73))

## [0.10.5] - 2025-11-25

### Bug Fixes

- Make server management operations synchronous and fix E2E tests ([`8382247`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/838224782c77e3189f0ea13ebc91bc4e403fddbb))
- Replace nil context with context.TODO() in tests ([`89351bf`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/89351bf2fbd675abdb8caf6517e25a2071b3166e))
- Replace deprecated skip-dirs with exclude-dirs in golangci-lint config ([`a328596`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a328596c6cef754547a80eec3fc46d97960c1f9a))
- Move exclude-dirs from run to issues section in golangci-lint config ([`cd51751`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cd51751d661e233b99cf5680e3294facd29622b1))
- Remove exclude-dirs section from golangci-lint config entirely ([`f1c8663`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f1c86633df6e772ea5822c8fd62fb0a6f420e2c8))
- Upgrade golangci-lint from v2.5.0 to v2.6.2 ([`0c6f7a9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0c6f7a9a9922db619a7f0bd5fe22f8397d1ced68))
- Exclude specs files from linting with build tag ([`ac54c08`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ac54c08388b492bd11f07baa68db53744526834d))
- Complete field mapping in management service ListServers ([`17c47c2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/17c47c2768d67f2ceaafaa4bd0627659db3df706))
- Mount Swagger UI directly on main mux to resolve 404 error ([`90b417d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/90b417d240c291e5d1e018ae354941068572f183))
- Correct field names in doctor command output for upstream errors ([`3787035`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3787035b9baed96ec44b7cb0ec8a12105752dcbb))
- Add sync and delay before hdiutil DMG creation to avoid Resource busy error ([`6e5b641`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6e5b64147fedf21e4325692057d1ee4dcc7b2430))
- Remove problematic base_ref check in release workflow ([`ac0ef2b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ac0ef2bd5349662120814823054e4cc917398792))

### Features

- Add LISTEN_PORT environment variable support to E2E test script ([`31c8488`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/31c8488e8eee87906814217d65596e9f35efe657))

## [0.10.4] - 2025-11-22

### Bug Fixes

- Update hashFiles pattern in GitHub Actions workflows ([`76ffb58`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/76ffb58099def23cb40db8ba02890aa956239814))
- Use setup-go built-in caching instead of manual cache ([`18b3e89`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/18b3e8929d0802dbfadf8d09dcffd5768943468a))
- Implement proper Windows named pipe availability check ([`8d34b70`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8d34b70be0f244d226ba574b6e13db70fdfccc70))

## [0.10.3] - 2025-11-21

### Bug Fixes

- Resolve context timeout and memory leak in logs follow mode ([`ec53f3f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ec53f3f63cea1ea620b5cbd91d50a0cf782e7864))
- Correct data structure parsing for missing_secrets and runtime_warnings in doctor command ([`b5b9879`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b5b987919f56ab72f14fa602e01bd769bd00e744))
- Improve signal handling and server validation in upstream commands ([`f0eb040`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0eb040291627aadcdf0b6ab905fab6eecb04d32))
- Make follow mode respect --tail flag in upstream logs command ([`7361e9d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7361e9d0ac52f482e97e47fb07471d1db4d5ce1e))
- Parse zap logger format in GetServerLogs to prevent double timestamps ([`e958989`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e958989bad6a8d3e34423fee2502deb124a0814a))
- Update ServerController interface to match GetServerLogs signature ([`6dcac3f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6dcac3f70789c6f2ead099cd836afc208ada389b))
- Update CLI client to use structured LogEntry type ([`d44eda5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d44eda56990f10c5c19232a655861dc67b497f76))
- Remove deprecated read-test-data utility and add missing mock methods ([`ae8f564`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ae8f5644a1c183f5033d876503b0a336d94cac1c))
- Remove redundant nil check for map length ([`ae21268`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ae21268aaa67913ade7072824a3fc879745247f1))
- Eliminate Docker recovery notification spam ([`21c62bf`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/21c62bfc200c29e20b6c66fd7ef964dbe4eadefc))

### Documentation

- Reduce CLAUDE.md size from 43.6k to 26k chars ([`6d71493`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6d714930e79274119006ed3ebfd70adf7b582a60))
- Add CLI management commands design ([`e795349`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e795349f069146a11cd1882b017e56781e589edb))
- Add CLI management commands implementation plan ([`5fedb2b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5fedb2bda93dd2674e3707774d8cb8219f5f93f8))
- Add CLI management commands to CLAUDE.md ([`e2c154d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e2c154d76efc91561c94699567e626975125edf7))
- Add comprehensive CLI management commands reference ([`cae6930`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cae6930542c84a8f06d3230d2205dc903def27e5))
- Clarify that doctor command pretty output needs implementation ([`0ac4e96`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0ac4e96a04d9eb3a0def8959563b00e6c8e591fe))
- Add godoc comments for exported CLI functions ([`7c1d01b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7c1d01b7595e53182c1e9202e56ca392d4d9c842))

### Features

- Add confirmation helper for bulk operations ([`bdefd6f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/bdefd6f4190ba6178218f5b8a516c0c0a1b89bfc))
- Add 'mcpproxy upstream list' command ([`89b8e04`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/89b8e0434df4be304868cc5be529f8edc2526f8e))
- Add 'mcpproxy upstream logs' command with follow mode ([`d367901`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d3679012c978aca45856693986570a4fe2d90722))
- Add upstream enable/disable/restart commands with --all support ([`ec1712c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ec1712ca1b86fb9e73b04255a52a04b0f52e5aff))
- Add 'mcpproxy doctor' command (placeholder) ([`b4e813e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b4e813e74dc9e029fda53e6a7cb10f000267b19b))
- Implement pretty output formatting for mcpproxy doctor command ([`2269b14`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2269b1446f01b5bb4adc65cf6bf311444589862e))
- Sort upstream server list alphabetically for consistency ([`65fed41`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/65fed41db2f3c7a876855eb61529534745914afb))
- Add tool annotations and MCP sessions tracking to WebUI ([`ac0ca97`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ac0ca978aede0d994b991e59d0a7b6419c53d681))
- Track and display MCP client capabilities in WebUI ([`8bdf52c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8bdf52c2bda57c890f073ba2d9ab56fc74323987))

### style

- Use box-drawing characters for table separators ([`35eff6d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/35eff6dbb2ad9cbc4ec57ad9fd4045bd06f2c054))

## [0.10.2] - 2025-11-19

### Bug Fixes

- Resolve call tool and code exec client mode issues ([`179f88e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/179f88eac7e6d2f99ffd7d6eff5f0290eb1c916d))
- Resolve test failures in httpapi and upstream manager ([`5b59db7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5b59db7dc62edd88caac0f56e2db7f47fa6fc0d8))
- Correct field name mismatch in code execution HTTP API ([`4bd1a0b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4bd1a0b93af1709dd87750b9f1a7877a270cb155))
- Add .exe extension for Windows in CLI E2E tests ([`e042505`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e042505c3c2f12fa34da70d9aa7ed1ae25e8a01f))
- Rename TestConcurrentCLICommands to include E2E suffix ([`c2aece7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c2aece7a22a16cd98529cefbdd32bff730b25c7b))

### Documentation

- Add CLI client mode documentation ([`a80c99a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a80c99a4a807ec1a4bf3855134268346dad16883))
- Mark E2E tests as completed in plan ([`46f7683`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/46f7683d80d7d04513e8e22a80e5b3e077c5fe84))

### Features

- Add shared socket detection and dialer module ([`689becc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/689becc6fd4bbbdcc681d8970f15273090a1f7bc))
- Add /api/v1/code/exec endpoint for CLI client mode ([`a3dddb6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a3dddb6c82dcdc28710c49e071173f574e53a034))
- Add HTTP client for CLI daemon communication ([`1b1326e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1b1326e1fcbd816c52af7414b81171d7b600dceb))
- Enhance CLI client mode with better logging, faster timeout, and user feedback ([`d265bd9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d265bd99d053680a3c2088f68bca437a37145d1d))

### Refactor

- Add client mode to 'code exec' command ([`3277248`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3277248558f142d42974406edcf67fa2fd529a5c))
- Add client mode to 'call tool' command ([`7528533`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/752853370c147774f76b1fe2de19dad2b5865d2a))
- Use shared socket module in tray ([`68dc4eb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/68dc4eb53a0336bbc7ae66afc4dcf3e96343e395))

## [0.10.1] - 2025-11-16

### Bug Fixes

- Include code_execution parent calls in tool call history and add token metrics for nested calls ([`585df2f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/585df2f0823f45cc55607171d4eafaee99883d11))
- Make Monaco editor properly fill resizable container ([`4603a6d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4603a6dfa32d167c218702c2815aead52ab09e72))

### Features

- Add token metrics for code_execution parent calls and improve UI ([`86083cc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/86083cc728ac2129712ef340539714478b232ba4))

## [0.10.0] - 2025-11-16

### Bug Fixes

- Register code_execution tool in CallBuiltInTool and CallToolDirect methods ([`29d6b47`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/29d6b4740b8e78f70b8817eb2c9a1a689a7b3bb6))
- Allow 0 for code execution config defaults in validation ([`76f9d03`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/76f9d0398f45634d6bf50338481d75ab2f644090))

### Features

- Add JavaScript code execution tool with full observability ([`36899fa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/36899fad71d971bc3b673ff22585af0363a7b7b2))
- Add MCP session tracking and parent-child call linking ([`afe19cd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/afe19cd76e00c72c772e812c8a64cf1c7827151b))

## [0.9.28] - 2025-11-14

### Features

- Add Windows installer with Inno Setup and WiX support ([`42e9b06`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/42e9b0665e630338e27dc4d9f7e20bf7d3338901))

## [0.9.26] - 2025-11-09

### Bug Fixes

- Add static OAuth credentials support and HTTP trace logging ([`51e6895`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/51e689528136bbed2fb6f7df9a9394a613191ce0))
- Enable zero-config OAuth with public client PKCE ([`54936f3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/54936f32c19b765587524bec505f6fcc0b5dc53f))
- Prevent panic after DCR failure, add helpful error messages ([`45204db`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/45204dbb8264add98342243c1d1a5c9733536658))

### Features

- Implement OAuth scope auto-discovery (RFC 9728 & RFC 8414) ([`620cc11`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/620cc112af5d8afc79c9f447ac680b104777bc5d))

## [0.9.25] - 2025-11-04

### Bug Fixes

- **upstream:** Prevent goroutine logging after test completion in Docker recovery ([`ba1a133`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ba1a133565d19082c997e4f113a84b66c090de45))
- **upstream:** Prevent goroutine logging in Docker recovery monitor on Windows ([`5428401`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5428401bc23cf79f41131709d98e21e6d9f94001))
- **upstream:** Prevent logging after context cancellation in Docker recovery retry ([`96ae306`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/96ae30602fb6ca2112daa5b0e734428938b9328c))
- **upstream:** Prevent OAuth event monitor logging after context cancellation ([`6323250`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/632325066e7af9d0ec8030b27409eb5bd2922cfe))
- **upstream:** Use parent context for shutdown detection in Docker recovery ([`bec9617`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/bec9617897954c60ee9ce2259f3f872e283c134b))
- **upstream:** Use parent context in Docker recovery timer checks ([`b50e7bc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b50e7bcdbede1f35e9c65bd26778d9886d68d48d))
- **tray:** Remove Docker dependency and always launch core ([`8e72161`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8e721612393a5d8046b5f418f8cbdff57de6b337))
- **upstream:** Use ctx.Err() instead of select-default for context checks ([`8a7f8a4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8a7f8a4d547f8b28fa5b54a2fdd29749bf1105fb))
- **upstream:** Add WaitGroup to track background goroutines during shutdown ([`432077a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/432077a11eeac6d7135fd0f3f9435612bcd58ff0))

## [0.9.24] - 2025-11-03

### Bug Fixes

- Ensure all Docker containers are cleaned up on application exit ([`e424b37`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e424b3717291404aba396ccc99ab3f2e11a1d4ee))
- Add missing ConnectionStateRecoveringDocker to stub file ([`19d30aa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/19d30aad9cdf4bb187850036a0ec95570a06dedb))
- **tray:** Fix shutdown bugs causing orphaned Docker containers ([`207a68e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/207a68ecfd7f72094ed3e5b2118ab8e7048d5f1c))
- **windows:** Move Unix-specific process functions to platform-specific files ([`acc5161`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/acc5161e2f37b97b8aec35c1210f0ca997bc8a7c))
- **tray:** Correct build constraints for desktop-only tray application ([`59c1209`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/59c1209fe7153f39d9ec60e2f2cfe19cf1528d0a))

### Documentation

- Critical analysis of Docker recovery implementation ([`4b8c84e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4b8c84e12fe7c15deabe9935854e06ddae35b067))
- Add Issue #11 - duplicate container spawning prevention ([`abc5dd4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/abc5dd491c9ead2ffaee61fece63ec20c3a05822))
- Add comprehensive Docker recovery documentation (Issue #10) ([`6b1c1f0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6b1c1f066868f674465992d16c498bdfedce386c))
- Add comprehensive shutdown bug analysis and fixes ([`6675c0c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6675c0ca77515c56806f978642bda9cb85cb3fdf))

### Features

- Implement Phase 1 - critical Docker recovery improvements ([`ead9be8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ead9be84666de2f8ccba7ba7b814451f1ef67e1a))
- Implement Phase 2 - reliability improvements ([`1acd9e2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1acd9e2910b5267ad22806f297cf13afdb80c365))
- Add system notifications for Docker recovery events (Issue #6) ([`de9b841`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/de9b841c01a812b4e7f5f224d62f6e763f00f7c2))
- Add persistent Docker recovery state across restarts (Issue #5) ([`a9c2ed2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a9c2ed26d7bcae353954ed27f98a6ce89f39a25c))
- Add configurable Docker recovery health check intervals (Issue #7) ([`d3c7e7d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d3c7e7d1baee4aef851a325dc5333cd4a7bcf10f))
- Add comprehensive metrics and observability for Docker recovery (Issue #8) ([`850e7c9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/850e7c92b6dabbde03b19a64fa710d07328d81a1))

## [0.9.23] - 2025-11-01

### Bug Fixes

- Ensure tray app exits cleanly on Quit ([`e69753a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e69753aaaafce8caa6dda151eb0568c2451381b7))
- Make OAuth client_id optional to support Dynamic Client Registration ([`a3b287e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a3b287ee43a9ff2a93695836c0344cd89e401a57))
- Remove empty if block to pass staticcheck linter ([`84fb408`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/84fb408b22ce82ff781e0f5ed307e2c80eb931c1))

### Features

- Improve OAuth token refresh with proactive grace period ([`716fcdc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/716fcdc704abf8708be6593c45e265652714f91c))

### Performance

- Optimize GitHub Actions workflows for faster PR feedback ([`e755ae2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e755ae20236d0227d06cd35b30212a372231feb1))

## [0.9.22] - 2025-10-31

### Bug Fixes

- **tray:** Prevent panic on double-close in health monitor ([`2dd7e8b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2dd7e8b6efd68a7a533a60ad27d208e9bc5df77c))
- **cli:** Make tools list command use managed client (same as serve mode) ([`1033984`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/10339842cc49de293a0e9a5b8069ec40fa354875))
- Clear OAuth error flags when transitioning to StateReady ([`036f74b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/036f74b8e991abf45621d0513e812e5035431d48))

### Features

- Implement non-blocking tool count reads for /api/v1/servers endpoint ([`66f5b4f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/66f5b4fb17561e4c4ba5d7482825dd48c7b715bf))
- Fix Web UI re-rendering and scroll position loss ([`21d2f96`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/21d2f9633da123f75e5e9aabb268213b388040d3))

## [0.9.21] - 2025-10-29

### Bug Fixes

- **upstream:** Improve SSE stability and OAuth timeout for remote scenarios ([`503b971`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/503b971ba7c3c0ecb5b4a03ad87c1c0021347e3c))
- **upstream:** Add SSE OnConnectionLost handlers and remove unused code ([`fea8e1c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fea8e1c380385e3f9c9d4d80186c9aea0e3074c1))
- **tray:** Fix shutdown sequence to prevent SSE reconnection and ensure clean exit ([`0a750f9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0a750f9fc65465dab2da4952296135e44459067f))
- SSE transport with TeeReader to prevent stream consumption ([`2e4af05`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2e4af056482c5cac02758ba5d8cd2230824ba5f8))
- **quarantine:** Non-blocking inspection with circuit breaker (issue #105) ([`8221ef7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8221ef74d706df96ba6bbf2f2e845056ee835753))
- **supervisor:** ActorPoolSimple.ConnectServer() now actually connects servers ([`1420c1b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1420c1b899884a7b861c44c6468dde49c39548b3))
- **sse:** Fix SSE stream context lifecycle and add request serialization ([`442269a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/442269a503fe37e780bd0dc8afed2c05e56615b3))
- **ci:** Fix E2E test and Windows npm install failures ([`f43b290`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f43b29070cbffa9e04c436b65a5b0432ed8f481f))

## [0.9.19] - 2025-10-27

### Bug Fixes

- Add permissions for PR comment workflow ([`0478258`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/04782580b6bb90e449fea6a1d610c371578d69c0))
- Include mcpproxy-tray binary in Windows archives ([`8eae28c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8eae28c18e845b1bdee24a22deac1ae30b81e62c))
- Use bash shell explicitly for Windows runner compatibility ([`471d75b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/471d75b8803f2cde6a76bb872f24cbb3006b89de))
- Add .exe extension for Windows E2E test binaries ([`f0fe3d3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0fe3d31870e6ca34236e198448635c60a4adf76))
- Skip tests for Windows ARM64 cross-compilation ([`2896e00`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2896e00bcde19efc61ce221e0abe80f76e8739fd))
- Use PowerShell Compress-Archive for Windows zip creation ([`b707d5b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b707d5b6b3eb3b8eea0b805d1dbe532b853c91cb))
- Properly format file list for PowerShell Compress-Archive ([`b04a5c0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b04a5c040c560b073750b7b1a8b56625b8023ed9))
- Skip internal/server E2E tests on Windows (timeout after 11min) ([`4ac225e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4ac225ec184624716939e951b1676d73457d8922))
- Add shell: bash to test step to ensure bash execution on Windows ([`41dc8a0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/41dc8a0c5d9a26674d7f85e79b71a1d87bb93aae))

### Features

- Enhance PR build workflow with dynamic versioning and artifact sharing ([`d673ae5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d673ae500adfd10c084bc27e6e63a4b74b1cda6a))
- Improve PR artifact download instructions ([`d673704`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d6737046b60ab6efbca16a3f906dc3e053cb5b2c))
- Align PR builds with release workflow using app bundle DMG ([`6e7300a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6e7300a1859428e92636636c1b38800e6e87490c))

### Refactor

- Streamline autostart functionality by consolidating launch scripts and removing redundant environment setup ([`de2dac1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/de2dac10cc935702ba728241dab134b598fb4dc1))

## [0.9.17] - 2025-10-25

### Bug Fixes

- Correct syscall.GetsockoptUcred signature for Linux ([`3dbe6db`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3dbe6dbbdeaea0f2b3e13b7a914488ffd76feebf))
- Rename socket E2E tests to match skip pattern ([`f40794c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f40794c6cddbdb199ffd8162d09405ac1e179cf4))
- Add missing Windows dependency go-winio ([`1a3007e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1a3007ed1cadb04623bcddcdfa62a2c9b5803417))
- Windows build issues with named pipes and platform-specific code ([`73fe3f7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/73fe3f7b63a7f3aaf9d45e1e5a81a4fde3569fc6))
- Re-add runtime import needed for GOOS checks ([`d960fa1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d960fa1a4390fd18b7059f4b341289eed7f1961e))
- Skip Unix socket test on Windows in dialer_test ([`a5fbbdd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a5fbbddd7e039c46cac03c2dcc7bb1dcd1472dcb))
- Resolve golangci-lint issues ([`24d3316`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/24d3316c9d6255939d28262e578de3e521c472d0))
- E2E test failures - Windows binary path and data directory permissions ([`b532540`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b5325403f56e4f3e4f875cf00b3276cdba55ee9e))
- Socket E2E tests - macOS path limits and shutdown race conditions ([`66930bd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/66930bd81d044567fb38fe0f840db75707354030))
- Tray menu displays server list and correct status URL in API mode ([`7b3fac4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7b3fac483424ff951e2ea4051409a3103fd454a4))
- Support empty MCPPROXY_API_KEY to disable authentication in E2E tests ([`7aea31b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7aea31becf5564d1404e347b8d41e9966cc4572f))
- Add curl -s flag to suppress progress meter in TestSocketInfoEndpoint ([`b2b80dc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b2b80dc16dcc471a8306d66ab54cfc05d9a1d862))
- Remove auto-retry logic from error states to match CanRetry configuration ([`3e0f709`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3e0f70985620263fc97091c446018c2ecce0043a))
- Skip E2E tests in unit test step to prevent 2-minute timeout ([`92b1b07`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/92b1b0778888f91d6389417f7e230c43dceefca3))
- Skip E2E tests in unit-tests.yml workflow ([`79d57cd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/79d57cd475a11a17fff0d032ccecb7616c304f13))
- Skip Unix socket E2E test on Go < 1.23 ([`55f8ace`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/55f8ace35f4df5fb9e000580b94dcfc2853730fc))
- Resolve macOS Go cache conflict in PR build workflow ([`1f515f1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1f515f1f93b8cd2a6ee48ba6c6d89bbd39664cc6))
- Add missing Quit() method to tray stub implementation ([`cd731ec`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cd731ec2020b7533484178817f624cca5019a238))
- Skip Unix socket E2E tests on Go < 1.23 to prevent port resolution failures ([`6c98ad9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6c98ad9e8102e55b7444e690f0d6426983942ddc))
- Remove toolchain directive to enable Go version matrix testing ([`3cdc780`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3cdc780afb01692477de3f3d2010023938e55a70))
- Skip TCP tests in socket E2E when port resolution fails ([`f29e5ff`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f29e5ff802d6aca84077f5b6035b92397c473fb4))
- Skip TestE2E_QuarantineConfigApply when race detector is enabled ([`e4dbbb2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e4dbbb2ed2c0197d63690be20a5406876c3ffa36))
- Move raceEnabled variable to separate file to fix build errors ([`020a2ae`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/020a2ae703e142ac3eea7d7dd1815986b7dd14a7))

### Features

- Unix socket/named pipe communication for tray-core ([`a9e8869`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a9e8869abfda45be8ac83b59418e38d278092fa8))
- Complete Unix socket/pipe implementation with enhanced testing and documentation ([`32b9a5a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/32b9a5a2bcace8feedf256ac7eefd4d58ae58f51))

### Fix

- Remove config sync E2E tests that don't match current architecture ([`39d15f5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/39d15f5ec5dd5d4e5f05955baa71a1fbc6c59a46))
- Remove unused prepareIsolatedConfig function ([`dd8611d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dd8611d5387c9418476e8e62a59b9099a2b98bb1))

### Refactor

- Rename enable_tray_socket to enable_socket for generalization ([`b262fc5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b262fc5057aa104753046ca3a51cc49d4f1d560b))

## [0.9.15] - 2025-10-23

### Bug Fixes

- **logging:** Capture stderr output before MCP initialization ([`8fac1c3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8fac1c368e6cad144d296ce17ff272bdd60f4a1c))
- **ui:** Fix layout ([`f79aa74`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f79aa7465501db89d81db363e92ee5637c8dce96))

### Features

- **tray:** Add color icons for Windows ([`65ace89`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/65ace89ffce30a62ca70881b254883b375dc2dec))
- Add Windows support for mcpproxy-tray ([`a64e289`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a64e289382df51b917178c8c4c80fee620c4137f))

### Fix

- Data race in scanForNewTokens method ([`5997f24`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5997f245dfab8f07f8ad05fbf8abf9d4207df78b))
- Keyring secret resolution and reactive server restart ([`fc85dfe`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fc85dfea7f4bcf2a55aefcfda5e5dace86f1eccb))
- Race condition in TestNotifySecretsChanged_WithAffectedServers test ([`83b9fdc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/83b9fdc606008712b1b554d6cf71b834f453a12d))
- Windows database cleanup issue in secret tests ([`4f75021`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4f7502121bff047c004d15555056c0892fb446ab))

## [0.9.14] - 2025-10-22

### Fix

- Configuration auto-refresh in-memory sync and SSE event integration ([`ffead18`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ffead18d5a4b9245c89f21ef7fa0b093375102c6))

## [0.9.13] - 2025-10-21

### Fix

- Populate StateView with tools from background discovery ([`925e3ab`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/925e3ab5bc19cda50a38e8fd58cb80ffb0167e14))
- Implement reactive tool discovery for immediate UI updates ([`55b62f4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/55b62f44f8dcde33cdc4982edc05b2607c044ad0))
- Immediate OAuth server connection and real-time UI updates ([`684d739`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/684d7397463e95896748153a2b1e4070cce52fb7))
- Resolve data race in supervisor package ([`2047768`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2047768a94feb7e8dd09ea90196a181b4e72fb1c))
- E2E tests failing due to improper supervisor reconciliation ([`0882db5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0882db52a9308f9fea434e0ab37938025d257dfc))
- TestE2E_ClientConnection timeout on macOS/Windows ([`7a490bf`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7a490bfc42393597525b6b899919b0609bec6cea))
- Web UI delete server functionality and add comprehensive tests ([`a5806ca`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a5806caaf593337a525fe2d9d0aa564a40f87706))

## [0.9.12] - 2025-10-19

### Fix

- Adjust tests for Windows platform atomicity limitations ([`37f10bc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/37f10bc1106a451f3c853b2d2b0207a85a2ead49))
- Suppress expected Windows errors in TestAtomicConfigWrite ([`ece8fd8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ece8fd818aa9a75148a6438cbc25d2d303f24b97))
- Suppress Windows read errors in TestAtomicConfigWrite ([`69e4dca`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/69e4dcad3e3d0048ce152cf2fac77f80a0ca7f94))
- Make tray error states persistent for better UX clarity ([`c631263`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c631263a3e49696c01f7115f30786b9c94b7ae18))

## [0.9.11] - 2025-10-16

### Fix

- Save config to disk when restart required (fixes listen address changes) ([`a5a3ce8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a5a3ce8206ff3ba5f35a7b06f6b7cd766b44e9c3))

## [0.9.9] - 2025-10-14

### Bug Fixes

- Handle empty config files including /dev/null ([`e9655af`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e9655afaf6017a7a26edbafd41b1474402430263))

### Features

- **runtime:** Implement Phase 3 Actor model for per-server goroutines ([`16109da`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/16109da5d9ef3329cc1f88e49360954870ca1a39))
- **runtime:** Implement Phase 4 Read Model & API Decoupling ([`ae24e50`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ae24e504cefd32430fc4d493527a2979ba4dc40d))
- **observability:** Implement Phase 5 Cleanup & Observability ([`f287589`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f287589d660e41f8aede35dd27ad41a74e12780f))
- **phase6:** Complete HTTP API StateView integration with async reconciliation ([`2c1def3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2c1def3f368adcbec7797cbcdcf3bdecfbfcc95b))

## [0.9.3] - 2025-10-10

### Bug Fixes

- Remove notarization check workflow file ([`07cb76e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/07cb76e53a37d4990e5df355669d716688f3346a))

### Documentation

- Update usage instructions for mcpproxy in workflows ([`65195b3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/65195b3c93764eeb6964a1ae463e59c55adf381a))

## [0.9.2] - 2025-10-10

### Bug Fixes

- Refactor macOS code-signing process and improve error handling for certificate imports ([`7030127`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/703012790e6d52ad2aa087f6d10e0c98c9fd4f2e))

## [0.9.1] - 2025-10-10

### Features

- Add step to copy frontend dist to embed location in release workflow ([`30fca3e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/30fca3e3d8f62af692e66cb612d5852abd96d0ae))

## [0.9.0] - 2025-10-10

### Bug Fixes

- **logging:** Capture stderr output before MCP initialization ([`0262e80`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0262e80d1a9b81125f86921e517fa3a5d839e33e))
- Format code and resolve linter issues ([`e36b192`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e36b192e05aa9560d9289b635312d7aaa31bd72c))
- Fix UI status display and data loading issues ([`fa01431`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fa01431e4f63eabed5ea495d81eda81304523838))
- Update default core URL to use 127.0.0.1 for consistency ([`32d3b03`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/32d3b036e994ec1bff0b79ffe95fdf670886e6dc))
- Add frontend build step to prerelease and release workflows ([`7c14ea3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7c14ea3241a4401079f0eef7a2f58dd16abb8757))
- Remove problematic Node.js cache configuration ([`91a5aa2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/91a5aa24e5bd157bd2102dacb7009879f96d3944))
- Include package-lock.json for reproducible builds ([`129b14d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/129b14dd8043d398ddb22b65d8e8e2ffa4c020fb))
- Correct frontend build output path for Go embed ([`1214104`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/121410425bc6f5abf4dae4910c7ab5fba002c803))
- Temporarily disable ESLint to fix CI workflows ([`9314a38`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9314a38f29d987e0d1b4611fcd545daef630f10c))
- Format stub files with gofmt ([`1a6d518`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1a6d5180aeedd406eb625479bd48a128363fea30))
- Resolve E2E test failures with API key authentication ([`9b386c1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9b386c1956f71dc42660a3e443cafe57db082558))
- Resolve TypeScript error in frontend API service and E2E test issues ([`ac7f8a5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ac7f8a52586c4203c439ce4b9d4d5ff91a1a181c))
- Apply linting fixes for better code quality ([`6b6c870`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6b6c87098a41255bea9c0544cf94352b368fe7ca))
- Update test to expect 127.0.0.1:8080 and fix linting issues ([`7046021`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7046021f75f18907a92ab8f4b72af8a7756f1336))
- Prevent tray from killing healthy core servers after 30 seconds ([`24f52a2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/24f52a27d16b55b0067a6cba6c5021a0c55e9cf1))
- Add /readyz endpoint and proper tray startup logic to prevent premature server kills ([`2211639`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/221163999234a1acac5db2422083b71f4cae5177))
- Resolve tray startup timeout issue with proper readiness checking ([`a7e8aad`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a7e8aadb11c6950c96c5d815dc8c84d57043c2da))
- Implement relaxed readiness criteria to prevent tray timeout issues ([`340c2ec`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/340c2ec3bbe9172db3038128f537718ed97af390))
- Improve HTTP API logging and tray reconnection visibility ([`cd4b09d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cd4b09d4279a489a5df1f2fca2168c2c672b0483))
- Resolve SSE endpoint streaming and Web UI authentication issues ([`f965eac`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f965eac0e07fbeb6692e4a05ee92e4ab0b98d7af))
- Improve SSE endpoint to handle non-flusher environments ([`e6a32bd`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e6a32bd7ab867314d5772b12234e08c9fb5b3176))
- Enhance SSE connection persistence with proper flushing and timing ([`af3557c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/af3557cc95117a6ba05db52cdb65f3ab6c58fee5))
- Implement localStorage persistence for Web UI API key ([`c7a23b0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c7a23b06144d84a3f6555d274f93230b3fbaf43a))
- Implement keyring placeholder resolution for secrets in server configs ([`361080d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/361080dd56a148e2ca20ef23c33e5559c3010c42))
- Prevent multiple DMG versions in prerelease artifacts ([`9ba6c14`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9ba6c1472d9b90c34e061529e8aef7f802f64bec))
- Resolve linter issues across codebase ([`2552824`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/25528249c178bde17f6efee89310170b2d343b57))
- Resolve frontend API authentication header issues ([`4b8d1a4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4b8d1a46a84d755c72d77e16856f3ac319c75805))
- Prevent race conditions in API key initialization and simplify Web UI handler ([`427027c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/427027cc32a96b4a5176a9828c42c3a4c17346b9))
- Add missing PKG installer scripts and fix tray HTTPS/HTTP protocol detection ([`dda7692`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dda7692d5f7c38ae8d103b2da92906d3b4ca6a06))
- Improve PKG installer signing with proper certificate handling ([`b911e5e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b911e5e27191b7ad5636e895ac0f979aec80a13f))
- Handle Developer ID certificate types for PKG signing ([`1aaf495`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1aaf495a5601905833ef91b17afb96dac52eefc5))
- Use component PKG to avoid Gatekeeper warnings ([`47bb74e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/47bb74e6d2915380207ac3049d58374fd845ceb5))
- Improve database opening logic in BoltDB ([`37c3a80`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/37c3a80899109d72542968ab692b24ee30a19aad))
- Update timestamp in e2e-config and remove sensitive api_key ([`257b82b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/257b82be4b146e6340db2ef82027efcf1f87f2a9))
- Update golangci-lint action version to v2.5.0 ([`ce1eb36`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ce1eb3685d451ebd57aa3c585c919af188d3a7f0))
- Update golangci-lint action version to v7 ([`1f82b2a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1f82b2a3463d3b587155b2a3503c26ebaa00e39b))
- Correct frontend dist copying in Windows workflow ([`1854f4f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1854f4fd2177d476f1d5b7b8de6b9d0af0c2dcc0))
- Add frontend build steps to E2E test workflow ([`7bb99de`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7bb99de75ccda2e49ea504d5e7ae5c72b74df014))
- Correct frontend build directory in E2E workflow ([`5f3668a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5f3668a29e455bf74e618df27a1959d1dbdb6701))
- Copy frontend dist to web/frontend/ for Go embed ([`4b9a64b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4b9a64b5679b25aa557ac90f2639f7e31c6779f3))
- Create web/frontend directory before copying dist ([`a5e5ea3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/a5e5ea3ab58dcf7ad7a0fda90e8d97687c684546))
- Disable API key auth in binary tests to prevent timeouts ([`acc598b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/acc598bbca25340f7ce7db59547c4ae0073a0322))
- Remove deprecated --tray flag from CI workflows ([`f94458d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f94458df769990263ce2651f207d32efdbd830ad))
- Reduce OAuth authentication error log level in tests ([`fb6c22a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fb6c22a47338cb2b05f3c37cb79d41f7599386be))
- Update server references from "everything" to "memory" in tests and related functions ([`3cffc76`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/3cffc765e7598ca44bee9b9e91cc02933bdaef01))
- Update test workflow to skip Binary tests and add dedicated Binary test execution ([`b10bc61`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b10bc615ab814ad6be4c58b6aa860b107056501d))
- Remove tools_stat reference from documentation and enhance tool indexing wait logic in tests ([`ccce7c5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ccce7c5bdbb1a08df80cbe7c32d833bee6895510))
- Improve waitForToolIndexing timeout and error handling ([`8bdbae6`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8bdbae669aeef2eada163747cbc8add914d31b50))

### CI/Build

- Add mcpproxy-tray binary to build process and clean up artifacts ([`b850e89`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b850e89151d2789342e5d6b31c9597623a86575f))
- Retry component PKG build after cache failure ([`835b618`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/835b6187e8ca0e2e9d20d1a8197f411cf2cc447d))

### Documentation

- Add prerelease build documentation and update workflow status ([`aabe336`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/aabe33679b4378f60bc1724b7c223c91a5e7eea3))
- Update CLAUDE.md and enhance APIService with API key management ([`43812e1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/43812e17284af81f1ffd07b51ace2735cb66ba1b))
- Update CLAUDE.md with new tray-core communication environment variables and API key management details ([`08f09d2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/08f09d282da4756bb030d4c1d0a7e68d03bcda9d))

### Features

- **tray:** Add color icons for Windows ([`bebc75f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/bebc75f2c1d0fdc4bc2f30688816b999a114cebf))
- **build:** Build script for Windows PowerShell ([`e6c154a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/e6c154a66deb31c5a895145a960b2471407af915))
- Implement secrets storage & UX with OS keyring integration ([`dd97d02`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dd97d0238a8578e1c1c2d085cf5f778bd1e6ae73))
- Add secret management API endpoints and types ([`c66468a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c66468a65af3325c48ef709004bde3f8fa154a15))
- Enhance secrets management with configuration secrets API ([`d3f0587`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d3f058734915820f3283baf1ce09c9176896d56d))
- Implement port conflict resolution in tray UI ([`16341e5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/16341e59537c1da7a07c39710755ac9c9a2ae565))
- Enhance tray configuration with environment variable support ([`b5c48cc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b5c48ccfdb5d6a337ab6f6ccf20796c7b0e1a069))
- Add feature flags to e2e configuration ([`dd3823e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/dd3823e02d8285dd486f98ba4985922df1389579))
- Fix prerelease DMG builds with proper tray binary support ([`ab31add`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ab31addb6c43920405ed0928f5b04b08167c3678))
- Add tool count caching to reduce excessive ListTools calls ([`597dfa5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/597dfa58fed4deb6c2454430deefabd8a8ca9d4c))
- Add security features - localhost binding and API key authentication ([`2be5068`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2be5068e4a78db8b68508e96a671dd729ecd7675))
- Improve web UI auth flow and API key handling ([`fa87c82`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fa87c82df7f9ed6505c9d3ffb5f943500235d5d5))
- Add TypeScript type generation step to frontend build process ([`5da0925`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/5da0925316838583d98a30f05b53f57643a0e3b2))
- Add secret management types and enhance frontend UI ([`cd1e364`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cd1e36484eb64a6ba2afc68d150c39a656aff183))
- Enhance Web UI authentication flow and security measures ([`4f88596`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4f885965c21f5539be3f655f05c845a22faa370a))
- Implement optional HTTPS support and certificate management ([`655ce54`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/655ce54871eb9eddb0fa04a892f453f433ec60e6))
- Add macOS PKG installer creation and notarization process ([`b5605ea`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b5605ea9154089c9be3d30799f18db7ee4c88629))
- Add Repositories page and copy MCP address functionality to NavBar ([`c07e47a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c07e47a63086adebae799fe220cbb4368c6de4cc))
- Add diagnostics API and UI integration ([`81ef188`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/81ef1884b478ddd187e9162412766a9290b5a66b))
- Implement Tool Call History feature ([`13bcdda`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/13bcdda561723fc5f4d97e56243f561065d9749e))
- Add HintsPanel component and integrate into multiple views ([`758f8a4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/758f8a410e9c80551b2ef67d3be25cf8a8899a8f))
- Add configuration management endpoints and UI integration ([`c4cfc52`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c4cfc52b82dd305ec5dacb1ea497f5fc80c6184d))
- Add tool call replay functionality ([`ba811fa`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ba811fa1b796b53cdacc6a368f16ae791d3b1676))
- Implement token metrics tracking and UI integration ([`4ad01d4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4ad01d499f6c8636c596cb5a96b5d6632680e11c))
- Enhance server loading with concurrency support ([`4b8329b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/4b8329b2fc9a71f37d351388b9e1e4a1172ba9ac))
- Add JsonViewer component for enhanced JSON display ([`12ddfbc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/12ddfbc439a15879ae74b5ac438aea96675b1bf1))
- Enhance UI with new components and chart integration ([`7add75f`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/7add75f47be83d2e24543a3deea77e3d7574963d))
- Update default configuration values for listener and TLS settings ([`13d258b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/13d258b087964e7499f07c05f1b7d159efe5ba4a))
- Add quarantine state management tests for server configuration ([`317be6c`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/317be6c593d4bbbec673a3aba988a7aeb6677a95))
- Update configuration and log directory paths to be platform-specific ([`ff7c944`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ff7c9444602451178aa706b37e2cdbc371062af0))
- Update installer branding and welcome messages for MCP Proxy ([`4436514`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/443651447b4c2468f9f62ec6709c0f37ab2b6635))
- Refactor SearchResult structure and update related components for improved tool data handling ([`fad192e`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fad192efcec2e1e080917d9b9f19afbd9c984353))
- Implement registry browsing functionality (Phase 7) ([`8973364`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8973364c21324d8a6335aeb733513ba7a070ee79))
- Rename 'Settings' to 'Configuration' and update related paths in SidebarNav and router ([`b621c2a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b621c2a051fe52ad576924c8b5fb6060e8494b60))
- Add CollapsibleHintsPanel component and integrate it into various views ([`93272e2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/93272e210cd99708910a341eefeeb1014697653b))
- Increase dropdown width in NavBar and SidebarNav for better usability ([`85e9280`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/85e9280734d7c689ee748006a842ab349bb6a5e8))
- Add welcome and conclusion RTF files for installer resources with fallback handling ([`45c7059`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/45c70598c7dd9d4c41e97b12b9b4f019b06ef830))
- Refactor tray management by removing startStopItem and related lifecycle controls ([`6afb2b7`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/6afb2b7a655d9a72eb4ece04f19597c8c80e84f7))
- Implement quarantine and unquarantine functionality for servers with UI integration ([`12b2b19`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/12b2b196f200ccd4c9007cb8101aa44b8b4accec))
- Reduce logging noise by removing active and idle connection state logs ([`88c269d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/88c269d501b27c3c4d8f27eb4d8314d7a90d5153))
- Enhance LoadConfiguredServers to accept a config parameter and improve async server reload handling ([`724c5c9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/724c5c9d30d7f8230afbabe6918ce13021063611))
- Enhance secret management by adding KeyringSecretStatus and integrating it into the configuration response ([`22915e4`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/22915e4bec09eb024eca38641b44237e8e3676ae))
- Enhance AddSecretModal with predefined name handling and improve Secrets view for missing secrets ([`b8cdeee`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/b8cdeee63e31714ce87e16edf9690c1fa5558363))
- Update NewClientWithOptions to use resolved server config for environment variables ([`50a0495`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/50a0495a615abffee3ea6e8d072d3dda8a924862))
- Refactor web package to remove development mode and embed frontend files ([`f3f7527`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f3f75279a277d74e3fd9717ef74797eda78e6bc1))
- Update embedded file handling to serve from frontend/dist directory ([`388bfa2`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/388bfa2f4d5a7060d0722bfcdc950053a65c5072))
- Update web handler to serve index.html from embedded filesystem and adjust .gitignore for new paths ([`51cdb22`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/51cdb22e49568f2ed6eab075f9b2fe85d7a6b641))
- Add new icon files and scripts for Windows tray support and build process ([`ba195bc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/ba195bc0bc3143b7a83a88eb6b32a5e2ccd1829d))
- Add step to copy frontend dist for embedding ([`2321fc3`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/2321fc33934304d3dcfc29d9d303a86238f3f53f))
- Add TypeScript types for API responses and server configurations ([`8d0f9be`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/8d0f9bed76edf6c65d7bfbb8aa963efa4e8b7f3a))
- Enhance frontend dist copying for embedding across OS environments ([`c9d5992`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c9d5992d754a99e97433b4d18e3a9fdf469c8b7a))
- Update Go version matrix in unit tests workflow ([`1cc9090`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/1cc9090eb168fa55f7035d623f9f38aa62529ce5))
- Implement WaitGroup for graceful shutdown in AsyncManager ([`d0af7a1`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d0af7a13f2efbf82fd88d5dd1e27f9ca5f2cd2e6))
- Implement platform-specific address-in-use error detection for server ([`fc3fba0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fc3fba0d5c02ff6ba079194f9cf825b56de9b8ec))
- Add RestartServer method for synchronous server restart functionality ([`df02a51`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/df02a5122a7cfe16aa297b0ba541d10d32294d08))

### P7

- Implement Interface Architecture & Dependency Injection ([`9b4100b`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9b4100b8ff5967205cee16e7d2eb21c622650cbe))

### Refactor

- Implement robust state machine architecture for tray app ([`cd6ef7d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/cd6ef7d05ce98bc233d46071c5e3279e30dc0094))
- Enhance core process launch handling in tray app ([`54e40dc`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/54e40dcc0a13f3a54e7255293f3d929cf8155f45))
- Update cache manager adapter and improve test logic for security checks ([`30fb813`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/30fb813da19324f4540b027fab5ccdd57ec57b5d))
- Update LoadConfiguredServers to use asynchronous server operations and modify e2e-config for enabled state ([`0e8a835`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/0e8a8352f8f526cd543bb144692a62b35a9aa440))
- Clean up unused code and comments for future functionality ([`10061fb`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/10061fbe761fea11240aee335f0dbd7341efb672))

### debug

- Add frontend build output debugging to workflow ([`9ac2e59`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9ac2e59a25a09dfe96c926cc222ee561dc2ce6ee))
- Add directory listing to troubleshoot frontend build ([`68429fe`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/68429fe8b024db7de8a662716709e2149a5d0d44))

### style

- Improve code formatting and consistency across multiple files ([`366a83a`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/366a83a607d94e2db43af4dba3fad7c3c9891fc9))

### tray

- Improve startup orchestration ([`034a0b8`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/034a0b81cd7ddb7d23766f89927efeb004fb055a))

## [0.8.6] - 2025-10-04

### Bug Fixes

- Correct display tray icon on windows ([`9cea772`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/9cea772d4bfb0f6af22b0b10cb8652706a8515d0))
- Run windows shell command ([`fc5a508`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fc5a5082f6bb64e17d8a9d9248a39c2a584b39d8))
- Define osWindows constant and update shell command logic ([`fe4b8f9`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/fe4b8f943365b1b63a53901025dee5f009e382b0))

## [0.1.8] - 2025-06-18

### Features

- Implement professional macOS DMG packaging and enhanced auto-update - Add macOS universal binary build configuration - Create GitHub Actions workflow for DMG creation using create-dmg - Implement comprehensive macOS installation guide - Add LaunchAgent plist for auto-start functionality - Enhance auto-update to handle ZIP files and universal binaries - Add proper asset finding logic for macOS universal binaries - Include Gatekeeper bypass instructions and security considerations - Support both drag-and-drop DMG and Homebrew installation methods ([`d888f40`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/d888f40f50a9f486395bbef6422844624bd39e48))

## [0.1.7] - 2025-06-18

### Bug Fixes

- Remove DMG configuration - DMG creation requires GoReleaser Pro, not available in free version ([`19f8343`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/19f83431c3aed20f1bc915a00a02efbe2f0cf66a))

## [0.1.6] - 2025-06-18

### Bug Fixes

- Correct GoReleaser v2 dmg configuration - Change dmgs to dmg field and simplify configuration for v2 compatibility ([`51c15c5`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/51c15c5fef5a3b024473af02a20adc3b65aa8df4))

## [0.1.5] - 2025-06-18

### Bug Fixes

- Update GoReleaser config for v2 compatibility - Replace deprecated brews with homebrew_casks, change folder to directory field, remove unsupported title field from winget pull_request, move dmg to dmgs section, add quarantine removal hook for unsigned binaries ([`f0b97f0`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/f0b97f0528486ef46cd7a7285992101016b3e5cd))

## [0.1.4] - 2025-06-18

### Bug Fixes

- Update GoReleaser action to v6 to support config version 2 ([`c81d52d`](https://github.com/smart-mcp-proxy/mcpproxy-go/commit/c81d52d12b21ba68ab654867a2e498dcc8e8e77c))

## [0.1.0] - 2025-06-18

---
[Full commit history](https://github.com/smart-mcp-proxy/mcpproxy-go/commits)
