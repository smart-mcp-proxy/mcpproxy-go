# Data model — Spec 121

**InstallChannel** (closed enum, string): `brew | dmg | winget | scoop | windows-installer | deb | rpm | docker | registry | curl | tarball | go-install | unknown`.

**Detector ID → InstallChannel** (total): homebrew→brew; dmg→dmg; deb→deb; rpm→rpm; docker→docker; go-install→go-install; windows-installer→windows-installer; tarball→tarball; scoop→scoop; unknown/anything else→unknown.

**Declaration sources** (in precedence order):
1. `MCPPROXY_INSTALL_CHANNEL` env — any enum member except `unknown`.
2. `<exe dir>/install-channel` — installer-owned; one token, ≤64 bytes, trimmed; allowed: `winget`.
3. Detector (mapped).
4. `<data dir>/install-channel` — weak; honoured only if (3) ∈ {tarball, go-install, unknown}; allowed: `curl`.

Invalid/oversized/unreadable → ignored (fall through). Never transmitted raw.

**Payload field**: `install_channel` (string, enum above, omitted only when telemetry builds a zero payload).

**Distribution manifests**: scoop JSON (version, architecture.{64bit,arm64}.{url,hash}, bin, shortcuts, checkver, autoupdate); winget 3-file set (version/installer/defaultLocale) owned by winget-releaser after bootstrap.
