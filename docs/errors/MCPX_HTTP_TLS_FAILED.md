---
id: MCPX_HTTP_TLS_FAILED
title: MCPX_HTTP_TLS_FAILED
sidebar_label: TLS_FAILED
description: TLS verification of the upstream server failed.
---

# `MCPX_HTTP_TLS_FAILED`

**Severity:** error
**Domain:** HTTP

## What happened

mcpproxy successfully resolved the hostname and opened a TCP connection, but the
TLS handshake failed (certificate verification, hostname mismatch, expired
certificate, or unsupported cipher).

## Common causes

- Self-signed certificate not trusted by the system store.
- Certificate expired or hasn't started yet (clock skew).
- Hostname doesn't match SAN entries on the certificate.
- Corporate MITM proxy with its own root not installed in the system store.
- Server only supports TLS 1.0/1.1 (mcpproxy requires 1.2+).

## How to fix

### Inspect the certificate

```bash
openssl s_client -connect <host>:443 -servername <host> -showcerts </dev/null
```

Look for `Verify return code:` and the certificate validity dates.

### Trust an internal CA

- **macOS:** open the `.crt` in Keychain Access → System keychain → set to
  *Always Trust*, or `sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain ca.crt`.
- **Linux:** copy to `/usr/local/share/ca-certificates/` and run
  `sudo update-ca-certificates`.
- **Windows:** import via `certmgr.msc` to *Trusted Root Certification Authorities*.

mcpproxy uses the OS trust store by default.

### Fix clock skew

```bash
# macOS
sudo sntp -sS time.apple.com
# Linux
sudo timedatectl set-ntp true
```

### Last-resort: skip verification (not recommended)

For a server you fully control, you can disable verification per-server. Do not
use this against the public internet.

```json
{ "tls_insecure_skip_verify": true }
```

## Related

- [`MCPX_HTTP_DNS_FAILED`](MCPX_HTTP_DNS_FAILED.md)
- [`MCPX_NETWORK_PROXY_MISCONFIG`](MCPX_NETWORK_PROXY_MISCONFIG.md)
