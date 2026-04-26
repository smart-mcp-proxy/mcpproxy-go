<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex items-center gap-4">
      <router-link to="/security" class="btn btn-ghost btn-sm gap-1">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
        </svg>
        Security
      </router-link>
      <div class="flex-1">
        <h1 class="text-3xl font-bold">Scan Report</h1>
        <div v-if="report" class="flex items-center gap-2 mt-1">
          <router-link
            :to="{ name: 'server-detail', params: { serverName: report.server_name } }"
            class="link link-primary text-sm"
          >{{ report.server_name }}</router-link>
          <span v-if="report.risk_score !== undefined"
            class="badge"
            :class="riskScoreClass"
          >Risk: {{ report.risk_score }}/100</span>
        </div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading scan report...</p>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error">
      <div>
        <h3 class="font-bold">Error</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="loadReport" class="btn btn-sm">Retry</button>
    </div>

    <template v-else-if="report">
      <!-- Metadata Card -->
      <div class="card bg-base-100 shadow-xl">
        <div class="card-body">
          <h2 class="card-title text-lg">Scan Metadata</h2>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mt-2">
            <div>
              <div class="text-xs text-base-content/50">Scan ID</div>
              <code class="font-mono text-sm select-all break-all">{{ report.job_id }}</code>
            </div>
            <div>
              <div class="text-xs text-base-content/50">Status</div>
              <span class="badge badge-sm" :class="statusBadgeClass">{{ reportStatus }}</span>
            </div>
            <div>
              <div class="text-xs text-base-content/50">Scanned At</div>
              <span class="text-sm">{{ formatDate(report.scanned_at) }}</span>
            </div>
            <div>
              <div class="text-xs text-base-content/50">Scanners</div>
              <span class="text-sm">{{ report.scanners_run ?? 0 }} run, {{ report.scanners_failed ?? 0 }} failed, {{ report.scanners_total ?? 0 }} total</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Scan Context Card -->
      <div v-if="scanContext" class="card bg-base-100 shadow-xl">
        <div class="card-body">
          <h2 class="card-title text-lg">Scan Context</h2>
          <div class="flex flex-wrap gap-2 mt-2">
            <span v-if="scanContext.source_method" class="badge badge-outline badge-sm">
              Source: {{ scanContext.source_method }}
            </span>
            <span v-if="scanContext.docker_isolation" class="badge badge-info badge-sm">
              Docker Isolated
            </span>
            <span v-if="!scanContext.docker_isolation" class="badge badge-warning badge-sm">
              Local (no Docker)
            </span>
            <span v-if="scanContext.server_protocol" class="badge badge-outline badge-sm">
              Protocol: {{ scanContext.server_protocol }}
            </span>
            <span v-if="scanContext.total_files" class="badge badge-outline badge-sm">
              {{ scanContext.total_files }} files
            </span>
            <span v-if="scanContext.container_image" class="badge badge-ghost badge-sm font-mono">
              {{ scanContext.container_image }}
            </span>
          </div>
        </div>
      </div>

      <!-- Threat Summary Stats -->
      <div class="flex flex-wrap gap-3">
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Dangerous</div>
            <div class="stat-value text-lg text-error">{{ report.summary?.dangerous ?? 0 }}</div>
          </div>
        </div>
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Warnings</div>
            <div class="stat-value text-lg text-warning">{{ report.summary?.warnings ?? 0 }}</div>
          </div>
        </div>
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Info</div>
            <div class="stat-value text-lg text-info">{{ report.summary?.info_level ?? 0 }}</div>
          </div>
        </div>
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Total</div>
            <div class="stat-value text-lg">{{ report.summary?.total ?? 0 }}</div>
          </div>
        </div>
      </div>

      <!-- Risk score disclaimer -->
      <div v-if="report.risk_score !== undefined" class="alert shadow-sm bg-base-200 border border-base-300">
        <svg class="w-5 h-5 text-base-content/50 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span class="text-xs text-base-content/60">
          The risk score is an experimental heuristic combining findings from multiple scanners using logarithmic aggregation.
          There is no industry standard for scoring MCP security risks. Treat the score as directional guidance, not a definitive safety assessment.
        </span>
      </div>

      <!-- Scan incomplete warnings -->
      <div v-if="report.scan_complete === false && report.empty_scan" class="alert alert-warning">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
        </svg>
        <div>
          <div class="font-semibold">No Files Scanned</div>
          <span>Scanners ran but found no files to analyze. The server may have been disconnected during source extraction.</span>
        </div>
      </div>
      <div v-else-if="report.scan_complete === false" class="alert alert-error">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <div>
          <div class="font-semibold">Scan Incomplete</div>
          <span>{{ report.scanners_failed ?? 0 }} of {{ report.scanners_total ?? 0 }} scanner(s) failed. Check scanner logs for details.</span>
        </div>
      </div>

      <!-- Clean state: no findings -->
      <div v-else-if="!report.findings || report.findings.length === 0" class="alert alert-success">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
        </svg>
        <span>No security issues detected. This server appears to be safe.</span>
      </div>

      <!-- Findings grouped by threat type -->
      <div v-else class="space-y-4">
        <h3 class="text-lg font-semibold">Findings</h3>

        <div v-for="group in groupedFindings" :key="group.type"
          class="collapse collapse-arrow bg-base-100 shadow-md"
          :class="{ 'collapse-open': group.defaultOpen }"
        >
          <input type="checkbox" :checked="group.defaultOpen" />
          <div class="collapse-title font-medium flex items-center gap-2">
            <span>{{ group.label }}</span>
            <span class="badge badge-sm" :class="group.badgeClass">{{ group.findings.length }}</span>
          </div>
          <div class="collapse-content">
            <div class="space-y-2">
              <div v-for="(finding, idx) in group.findings" :key="idx"
                class="collapse collapse-arrow bg-base-200 rounded-lg"
              >
                <input type="checkbox" />
                <div class="collapse-title py-2 px-4 min-h-0 flex items-center gap-3">
                  <span
                    class="badge badge-sm flex-shrink-0"
                    :class="{
                      'badge-error': finding.threat_level === 'dangerous',
                      'badge-warning': finding.threat_level === 'warning',
                      'badge-info': finding.threat_level === 'info',
                    }"
                  >
                    {{ finding.threat_level }}
                  </span>
                  <span class="font-medium text-sm flex-1">
                    {{ finding.rule_id || finding.title }}
                  </span>
                  <span v-if="finding.package_name" class="font-mono text-xs text-base-content/50">
                    {{ finding.package_name }}
                  </span>
                  <span v-if="finding.fixed_version" class="badge badge-xs badge-success badge-outline">
                    fix: {{ finding.fixed_version }}
                  </span>
                </div>
                <div class="collapse-content px-4 pb-3">
                  <div class="space-y-2 text-sm">
                    <p class="text-base-content/80">{{ finding.description }}</p>
                    <!-- Evidence -->
                    <div v-if="finding.evidence" class="mt-2">
                      <div class="text-xs text-base-content/50 mb-1">Triggering content:</div>
                      <pre class="bg-base-300 text-xs p-3 rounded-lg max-h-32 overflow-auto whitespace-pre-wrap break-words border border-base-content/10">{{ finding.evidence }}</pre>
                    </div>
                    <div class="grid grid-cols-2 gap-2 text-xs">
                      <div v-if="finding.rule_id">
                        <span class="text-base-content/50">Rule:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.rule_id }}</code>
                      </div>
                      <div v-if="finding.severity">
                        <span class="text-base-content/50">CVSS Severity:</span>
                        <span class="ml-1 font-medium">{{ finding.severity }}</span>
                        <span v-if="finding.cvss_score" class="ml-1">({{ finding.cvss_score }})</span>
                      </div>
                      <div v-if="finding.package_name">
                        <span class="text-base-content/50">Package:</span>
                        <span class="ml-1 font-mono">{{ finding.package_name }}</span>
                        <span v-if="finding.installed_version" class="ml-1 text-base-content/50">v{{ finding.installed_version }}</span>
                      </div>
                      <div v-if="finding.fixed_version">
                        <span class="text-base-content/50">Fixed in:</span>
                        <span class="ml-1 font-mono text-success">{{ finding.fixed_version }}</span>
                      </div>
                      <div v-if="finding.location">
                        <span class="text-base-content/50">Location:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.location }}</code>
                      </div>
                      <div v-if="finding.scanner">
                        <span class="text-base-content/50">Scanner:</span>
                        <span class="ml-1">{{ scannerDisplayName(finding.scanner) }}</span>
                      </div>
                    </div>
                    <a
                      v-if="finding.help_uri"
                      :href="finding.help_uri"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="link link-primary text-xs inline-flex items-center gap-1"
                    >
                      View Advisory Details &rarr;
                    </a>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Supply Chain Audit -->
      <!-- Pass 2 in-progress banner (shown independently of existing findings) -->
      <div v-if="report.pass2_running" class="alert alert-info">
        <span class="loading loading-spinner loading-sm"></span>
        <div>
          <h3 class="font-bold">Supply Chain Audit</h3>
          <p class="text-sm">Deep dependency analysis running in background. Additional CVEs will appear here when complete.</p>
        </div>
      </div>
      <!-- CVE/package findings from any pass (flagged by backend) -->
      <div v-if="supplyChainFindings.length > 0" class="space-y-4">
        <div class="collapse collapse-arrow bg-base-100 shadow-md">
          <input type="checkbox" />
          <div class="collapse-title font-medium flex items-center gap-2">
            <span>Supply Chain Audit (CVEs)</span>
            <span class="badge badge-sm" :class="supplyChainHasDangerous ? 'badge-error' : supplyChainHasWarnings ? 'badge-warning' : 'badge-info'">{{ supplyChainFindings.length }}</span>
          </div>
          <div class="collapse-content">
            <div class="space-y-2">
              <div v-for="(finding, idx) in supplyChainFindings" :key="'sc-' + idx"
                class="collapse collapse-arrow bg-base-200 rounded-lg"
              >
                <input type="checkbox" />
                <div class="collapse-title py-2 px-4 min-h-0 flex items-center gap-3">
                  <span
                    class="badge badge-sm flex-shrink-0"
                    :class="{
                      'badge-error': finding.threat_level === 'dangerous',
                      'badge-warning': finding.threat_level === 'warning',
                      'badge-info': finding.threat_level === 'info',
                    }"
                  >
                    {{ finding.threat_level }}
                  </span>
                  <span class="font-medium text-sm flex-1">
                    {{ finding.rule_id || finding.title }}
                  </span>
                  <span v-if="finding.package_name" class="font-mono text-xs text-base-content/50">
                    {{ finding.package_name }}
                  </span>
                  <span v-if="finding.fixed_version" class="badge badge-xs badge-success badge-outline">
                    fix: {{ finding.fixed_version }}
                  </span>
                </div>
                <div class="collapse-content px-4 pb-3">
                  <div class="space-y-2 text-sm">
                    <p class="text-base-content/80">{{ finding.description }}</p>
                    <div v-if="finding.evidence" class="mt-2">
                      <div class="text-xs text-base-content/50 mb-1">Triggering content:</div>
                      <pre class="bg-base-300 text-xs p-3 rounded-lg max-h-32 overflow-auto whitespace-pre-wrap break-words border border-base-content/10">{{ finding.evidence }}</pre>
                    </div>
                    <div class="grid grid-cols-2 gap-2 text-xs">
                      <div v-if="finding.rule_id">
                        <span class="text-base-content/50">Rule:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.rule_id }}</code>
                      </div>
                      <div v-if="finding.severity">
                        <span class="text-base-content/50">CVSS Severity:</span>
                        <span class="ml-1 font-medium">{{ finding.severity }}</span>
                        <span v-if="finding.cvss_score" class="ml-1">({{ finding.cvss_score }})</span>
                      </div>
                      <div v-if="finding.package_name">
                        <span class="text-base-content/50">Package:</span>
                        <span class="ml-1 font-mono">{{ finding.package_name }}</span>
                        <span v-if="finding.installed_version" class="ml-1 text-base-content/50">v{{ finding.installed_version }}</span>
                      </div>
                      <div v-if="finding.fixed_version">
                        <span class="text-base-content/50">Fixed in:</span>
                        <span class="ml-1 font-mono text-success">{{ finding.fixed_version }}</span>
                      </div>
                      <div v-if="finding.location">
                        <span class="text-base-content/50">Location:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.location }}</code>
                      </div>
                      <div v-if="finding.scanner">
                        <span class="text-base-content/50">Scanner:</span>
                        <span class="ml-1">{{ scannerDisplayName(finding.scanner) }}</span>
                      </div>
                    </div>
                    <a
                      v-if="finding.help_uri"
                      :href="finding.help_uri"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="link link-primary text-xs inline-flex items-center gap-1"
                    >
                      View Advisory Details &rarr;
                    </a>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <div v-else-if="report.pass2_complete" class="alert alert-success">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
        </svg>
        <span>Supply chain audit complete. No CVEs found in dependencies.</span>
      </div>

      <!-- Scanned Files: lazy-loaded list of every file the scanners actually
           saw, with suspicious-file markers and inline finding titles. This
           is the most important triage signal alongside the findings list —
           if the scanner only saw `tools.json` (tool_definitions_only mode),
           any "Malicious Code" finding here means very different things from
           the same finding on a real source tree of 100 files. Backend
           endpoint: GET /api/v1/servers/{id}/scan/files (already paginated).
           File CONTENT is not persisted — the temp source dir is cleaned up
           after the scan, so this viewer shows paths + finding attribution
           only. Restoring full content viewing would require persisting the
           extracted source alongside the scan record (separate spec). -->
      <div class="collapse collapse-arrow bg-base-100 shadow-md">
        <input type="checkbox" :checked="filesExpanded" @change="onScannedFilesToggle" />
        <div class="collapse-title font-medium flex items-center gap-2 flex-wrap">
          <span>Scanned Files</span>
          <span v-if="scanFilesMeta.total > 0" class="badge badge-sm badge-ghost">{{ scanFilesMeta.total }} {{ scanFilesMeta.total === 1 ? 'file' : 'files' }}</span>
          <span v-if="scanFilesMeta.suspicious_count > 0" class="badge badge-sm badge-error">
            {{ scanFilesMeta.suspicious_count }} suspicious
          </span>
          <span v-if="scanContext?.source_method" class="badge badge-sm badge-outline">{{ scanContext.source_method }}</span>
        </div>
        <div class="collapse-content">
          <div v-if="!filesLoaded && !scanFilesLoading" class="text-sm text-base-content/50 py-2">
            Click to load the file list.
          </div>
          <div v-else-if="scanFilesLoading && scanFiles.length === 0" class="text-center py-6">
            <span class="loading loading-spinner loading-md"></span>
            <p class="mt-2 text-sm text-base-content/60">Loading file list…</p>
          </div>
          <div v-else>
            <!-- Controls: pass selector + suspicious-only filter -->
            <div class="flex flex-wrap items-center gap-3 mb-3 pb-3 border-b border-base-200">
              <div class="join">
                <button
                  class="btn btn-xs join-item"
                  :class="scanFilesPass === 1 ? 'btn-active' : 'btn-ghost'"
                  @click="switchFilesPass(1)"
                >Pass 1 — Security</button>
                <button
                  class="btn btn-xs join-item"
                  :class="scanFilesPass === 2 ? 'btn-active' : 'btn-ghost'"
                  @click="switchFilesPass(2)"
                >Pass 2 — Supply Chain</button>
              </div>
              <label class="label cursor-pointer gap-2 py-0">
                <input type="checkbox" v-model="suspiciousOnly" @change="onSuspiciousFilterChange" class="checkbox checkbox-xs" />
                <span class="label-text text-xs">Suspicious only</span>
              </label>
              <span v-if="scanContext?.source_path" class="text-xs text-base-content/50 ml-auto font-mono break-all">
                {{ scanContext.source_path }}
              </span>
            </div>

            <!-- Empty / no-files states tailored to scan mode. The wording
                 here matters for triage — "no source files" + a finding on
                 tools.json should NOT make the user think the scan was
                 useless; it just means we ran the AI scanner on the tool
                 definitions only. -->
            <div v-if="scanFiles.length === 0" class="text-sm text-base-content/50 py-4 italic">
              <template v-if="suspiciousOnly">
                No suspicious files in this pass. Untoggle "Suspicious only" to see all scanned files.
              </template>
              <template v-else-if="scanContext?.source_method === 'tool_definitions_only'">
                No source files were extracted for this server — the AI scanner ran on
                the exported tool definitions only. Findings located at <code class="bg-base-200 px-1 rounded">tools.json</code>
                refer to that synthetic file, not real source code.
              </template>
              <template v-else-if="scanContext?.source_method === 'url'">
                URL-based scan — no local files. Scanners connected to the server endpoint directly.
              </template>
              <template v-else>
                No files in Pass {{ scanFilesPass }}.
              </template>
            </div>

            <!-- File list: terminal-style ASCII tree with suspicious markers
                 + inline finding-title chips. We don't try to render an
                 actual nested tree — paginated flat list keeps the
                 implementation simple and matches the previous version of
                 this viewer (commit 409ca437). -->
            <div v-else class="space-y-1 font-mono text-xs max-h-96 overflow-auto pr-2">
              <div
                v-for="(file, idx) in scanFiles"
                :key="file.path"
                class="flex items-start gap-2 py-0.5"
                :class="file.suspicious ? 'bg-error/5 -mx-2 px-2 rounded' : ''"
              >
                <span class="text-base-content/30 select-none w-4 text-right flex-shrink-0">{{ idx === scanFiles.length - 1 ? '└' : '├' }}</span>
                <span v-if="file.suspicious" class="text-error flex-shrink-0" title="File has at least one finding">●</span>
                <span v-else class="text-base-content/20 flex-shrink-0">○</span>
                <code
                  class="break-all"
                  :class="file.suspicious ? 'text-error font-medium' : 'text-base-content/80'"
                >{{ file.path }}</code>
                <div v-if="file.findings && file.findings.length" class="flex flex-wrap gap-1 ml-auto flex-shrink-0">
                  <span
                    v-for="(t, i) in file.findings.slice(0, 3)"
                    :key="i"
                    class="badge badge-xs badge-error badge-outline whitespace-nowrap"
                    :title="t"
                  >{{ truncateFindingTitle(t) }}</span>
                  <span v-if="file.findings.length > 3" class="badge badge-xs badge-ghost">
                    +{{ file.findings.length - 3 }}
                  </span>
                </div>
              </div>
            </div>

            <!-- Pagination: load-more button when has_more, plus a small
                 footer with totals. -->
            <div v-if="scanFiles.length > 0" class="flex items-center justify-between mt-3 pt-2 border-t border-base-200 text-xs text-base-content/60">
              <span>
                Showing {{ scanFiles.length }} of {{ scanFilesMeta.total }}
                <span v-if="scanContext && (scanContext.total_size_bytes ?? 0) > 0">
                  · {{ formatFileSize(scanContext.total_size_bytes) }}
                </span>
              </span>
              <button
                v-if="scanFilesMeta.has_more"
                class="btn btn-xs btn-ghost"
                :disabled="scanFilesLoading"
                @click="loadMoreFiles"
              >
                <span v-if="scanFilesLoading" class="loading loading-spinner loading-xs"></span>
                Load more
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Scanner Execution Logs -->
      <div v-if="report.scanner_statuses && report.scanner_statuses.length > 0" class="collapse collapse-arrow bg-base-100 shadow-md">
        <input type="checkbox" />
        <div class="collapse-title font-medium">
          Scanner Execution Logs
          <span class="badge badge-sm badge-ghost ml-2">{{ report.scanner_statuses.length }} scanners</span>
        </div>
        <div class="collapse-content">
          <div class="space-y-4">
            <div v-for="ss in report.scanner_statuses" :key="ss.scanner_id" class="border border-base-300 rounded-lg p-3">
              <div class="flex items-center justify-between mb-2">
                <span class="font-medium">{{ scannerDisplayName(ss.scanner_id) }}</span>
                <div class="flex items-center gap-2">
                  <span class="badge badge-sm" :class="{
                    'badge-success': ss.status === 'completed',
                    'badge-error': ss.status === 'failed',
                    'badge-info': ss.status === 'running',
                    'badge-ghost': !ss.status,
                  }">{{ ss.status || 'unknown' }}</span>
                  <span v-if="ss.findings_count" class="text-xs text-base-content/60">{{ ss.findings_count }} findings</span>
                  <span v-if="ss.exit_code !== undefined && ss.exit_code !== 0" class="text-xs text-error">exit {{ ss.exit_code }}</span>
                </div>
              </div>
              <div v-if="ss.error" class="text-sm text-error mb-2">{{ ss.error }}</div>
              <div v-if="ss.stdout" class="mb-2">
                <div class="text-xs text-base-content/50 mb-1">stdout</div>
                <pre class="bg-base-200 text-xs p-3 rounded-lg max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ ss.stdout }}</pre>
              </div>
              <div v-if="ss.stderr">
                <div class="text-xs text-base-content/50 mb-1">stderr</div>
                <pre class="bg-base-200 text-xs p-3 rounded-lg max-h-48 overflow-auto whitespace-pre-wrap break-words text-warning">{{ ss.stderr }}</pre>
              </div>
              <div v-if="!ss.stdout && !ss.stderr && !ss.error" class="text-xs text-base-content/40 italic">No output captured</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Server Status & Actions -->
      <div class="card bg-base-100 shadow-xl">
        <div class="card-body py-4">
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-3">
              <span class="text-sm text-base-content/60">Server Status:</span>
              <span v-if="serverStatus === 'loading'" class="loading loading-spinner loading-xs"></span>
              <span v-else class="badge" :class="{
                'badge-success': serverAdminState === 'enabled',
                'badge-warning': serverAdminState === 'disabled',
                'badge-error': serverAdminState === 'quarantined',
              }">{{ serverAdminState }}</span>
            </div>
            <div class="flex gap-2">
              <button
                v-if="serverAdminState === 'enabled' && report.summary?.dangerous > 0"
                @click="quarantineServer"
                :disabled="actionLoading"
                class="btn btn-error btn-sm"
              >
                <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                Quarantine Server
              </button>
              <button
                v-if="serverAdminState === 'quarantined'"
                @click="approveServer"
                :disabled="actionLoading || hasUnresolvedCritical"
                class="btn btn-success btn-sm"
                :title="hasUnresolvedCritical ? 'Unresolved critical findings — use Force Approve' : 'Approve and unquarantine this server'"
              >
                <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                Approve Server
              </button>
              <button
                v-if="serverAdminState === 'quarantined' && hasUnresolvedCritical"
                @click="forceApproveServer"
                :disabled="actionLoading"
                class="btn btn-error btn-sm"
                title="Bypass the scanner gate and approve despite critical findings"
              >
                <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                Force Approve
              </button>
              <button
                v-if="serverAdminState === 'quarantined'"
                @click="rejectServer"
                :disabled="actionLoading"
                class="btn btn-outline btn-warning btn-sm"
                title="Reject the scan and keep the server quarantined"
              >
                <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                Reject
              </button>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import type { SecurityScanFinding, ThreatType } from '@/types/api'

const serversStore = useServersStore()
const systemStore = useSystemStore()

const props = defineProps<{
  jobId: string
}>()

const loading = ref(false)
const error = ref('')
const report = ref<any>(null)
const actionLoading = ref(false)
const serverStatus = ref<'loading' | 'loaded'>('loading')
const serverAdminState = ref('unknown')

const scannerNames: Record<string, string> = {
  'mcp-ai-scanner': 'MCP AI Scanner',
  'trivy': 'Trivy',
  'cisco-mcp-scanner': 'Cisco MCP Scanner',
  'mcp-scan': 'MCP Scan (Invariant)',
}

function scannerDisplayName(id: string): string {
  return scannerNames[id] || id
}

// Scan context from the aggregated report (populated from job's ScanContext)
const scanContext = computed(() => {
  return report.value?.scan_context || null
})

// Status display
const reportStatus = computed(() => {
  if (!report.value) return 'unknown'
  if (report.value.scan_complete === false) return 'incomplete'
  if (report.value.empty_scan) return 'empty'
  if (!report.value.findings || report.value.findings.length === 0) return 'clean'
  if (report.value.summary?.dangerous > 0) return 'dangerous'
  if (report.value.summary?.warnings > 0) return 'warnings'
  return 'clean'
})

const statusBadgeClass = computed(() => {
  switch (reportStatus.value) {
    case 'dangerous': return 'badge-error'
    case 'warnings': return 'badge-warning'
    case 'incomplete': return 'badge-error'
    case 'empty': return 'badge-warning'
    case 'clean': return 'badge-success'
    default: return 'badge-ghost'
  }
})

const riskScoreClass = computed(() => {
  const score = report.value?.risk_score ?? 0
  if (score >= 70) return 'badge-error'
  if (score >= 30) return 'badge-warning'
  return 'badge-success'
})

// Threat type grouping. Real CVE/package findings are routed to the dedicated
// Supply Chain Audit section via the `supply_chain_audit` flag instead of the
// `supply_chain` threat type, so they are filtered out of `groupedFindings`.
// 'uncategorized' is rendered as "Other Findings" so AI-scanner output that
// ClassifyThreat can't pattern-match stays visible instead of silently vanishing.
const threatTypeLabels: Record<Exclude<ThreatType, 'supply_chain'>, string> = {
  tool_poisoning: 'Tool Poisoning',
  prompt_injection: 'Prompt Injection',
  rug_pull: 'Rug Pull Detection',
  malicious_code: 'Malicious Code',
  uncategorized: 'Other Findings',
}

type DisplayThreatType = Exclude<ThreatType, 'supply_chain'>
const dangerousTypes: DisplayThreatType[] = ['tool_poisoning', 'prompt_injection', 'rug_pull', 'malicious_code']

interface FindingGroup {
  type: DisplayThreatType
  label: string
  findings: SecurityScanFinding[]
  defaultOpen: boolean
  badgeClass: string
}

const groupedFindings = computed<FindingGroup[]>(() => {
  if (!report.value?.findings) return []

  // Pull out CVE/package findings; they render in the Supply Chain Audit section.
  // Everything else is grouped by threat_type regardless of which pass produced it,
  // so AI-scanner findings that only surface during the deep Pass 2 scan land in
  // their proper category instead of the CVE list.
  const nonCveFindings = report.value.findings.filter(
    (f: SecurityScanFinding) => !f.supply_chain_audit
  )

  const groups = new Map<DisplayThreatType, SecurityScanFinding[]>()
  for (const f of nonCveFindings) {
    const rawType = (f.threat_type || 'uncategorized') as ThreatType
    // Legacy data may still carry threat_type === 'supply_chain' on a non-CVE
    // finding. Fold it into 'uncategorized' so it stays visible instead of
    // being silently dropped.
    const type: DisplayThreatType = rawType === 'supply_chain' ? 'uncategorized' : rawType
    if (!groups.has(type)) groups.set(type, [])
    groups.get(type)!.push(f)
  }

  const result: FindingGroup[] = []
  const typeOrder: DisplayThreatType[] = ['tool_poisoning', 'prompt_injection', 'rug_pull', 'malicious_code', 'uncategorized']
  for (const type of typeOrder) {
    const findings = groups.get(type)
    if (!findings) continue
    const hasDangerous = findings.some(f => f.threat_level === 'dangerous')
    result.push({
      type,
      label: threatTypeLabels[type] || type,
      findings,
      defaultOpen: dangerousTypes.includes(type),
      badgeClass: hasDangerous ? 'badge-error' : findings.some(f => f.threat_level === 'warning') ? 'badge-warning' : 'badge-info',
    })
  }
  return result
})

const supplyChainFindings = computed<SecurityScanFinding[]>(() => {
  if (!report.value?.findings) return []
  return report.value.findings.filter((f: SecurityScanFinding) => f.supply_chain_audit === true)
})

const supplyChainHasDangerous = computed(() => {
  return supplyChainFindings.value.some(f => f.threat_level === 'dangerous')
})

const supplyChainHasWarnings = computed(() => {
  return supplyChainFindings.value.some(f => f.threat_level === 'warning')
})

function formatDate(dateStr: string): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return d.toLocaleString()
}

async function loadReport() {
  loading.value = true
  error.value = ''
  try {
    const res = await api.getScanReportByJobId(props.jobId)
    if (res.success && res.data) {
      report.value = res.data
    } else {
      error.value = res.error || 'Failed to load scan report'
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function loadServerStatus() {
  if (!report.value?.server_name) return
  serverStatus.value = 'loading'
  try {
    const res = await api.getServers()
    if (res.success && res.data?.servers) {
      const server = res.data.servers.find((s: any) => s.name === report.value.server_name)
      if (server?.health?.admin_state) {
        serverAdminState.value = server.health.admin_state
      } else {
        serverAdminState.value = 'unknown'
      }
    }
  } catch {
    serverAdminState.value = 'unknown'
  } finally {
    serverStatus.value = 'loaded'
  }
}

async function quarantineServer() {
  if (!report.value?.server_name) return
  if (!confirm(`Quarantine ${report.value.server_name}? This will disconnect the server.`)) return
  actionLoading.value = true
  try {
    await api.quarantineServer(report.value.server_name)
    await loadServerStatus()
  } finally {
    actionLoading.value = false
  }
}

// F-04: Go through the security-aware approval path instead of the legacy
// unquarantine endpoint. hasUnresolvedCritical disables the primary Approve
// button so the user must use Force Approve explicitly.
const hasUnresolvedCritical = computed(() => {
  return (report.value?.summary?.critical ?? 0) > 0
})

async function approveServer() {
  if (!report.value?.server_name) return
  if (!confirm(`Approve ${report.value.server_name}? This will unquarantine and re-enable the server.`)) return
  actionLoading.value = true
  try {
    await serversStore.securityApproveServer(report.value.server_name, false)
    systemStore.addToast({
      type: 'success',
      title: 'Server Approved',
      message: `${report.value.server_name} has been approved and unquarantined`,
    })
    await loadServerStatus()
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Approve Failed',
      message: err instanceof Error ? err.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function forceApproveServer() {
  if (!report.value?.server_name) return
  if (!confirm(`Force-approve ${report.value.server_name}? This bypasses the scanner gate despite ${report.value.summary?.critical ?? 0} critical finding(s).`)) return
  actionLoading.value = true
  try {
    await serversStore.securityApproveServer(report.value.server_name, true)
    systemStore.addToast({
      type: 'success',
      title: 'Server Force-Approved',
      message: `${report.value.server_name} was force-approved despite critical findings`,
    })
    await loadServerStatus()
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Force Approve Failed',
      message: err instanceof Error ? err.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function rejectServer() {
  if (!report.value?.server_name) return
  if (!confirm(`Reject the scan for ${report.value.server_name}? The server will remain quarantined.`)) return
  actionLoading.value = true
  try {
    const res = await api.securityReject(report.value.server_name)
    if (!res.success) throw new Error(res.error || 'Failed to reject scan')
    systemStore.addToast({
      type: 'success',
      title: 'Scan Rejected',
      message: `${report.value.server_name} remains quarantined`,
    })
    await loadServerStatus()
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Reject Failed',
      message: err instanceof Error ? err.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

// --- Scanned Files viewer ---
// File viewer is lazy-loaded on first expand to avoid pulling potentially
// 10K+ entries on every report view. Backend is paginated; we load 100 at
// a time and surface a "Load more" button when has_more is set. Only the
// file LIST (paths + per-file finding attribution) is shown — file content
// is not persisted alongside the scan record (the temp source dir is
// cleaned up after the scan), so a content viewer would require either
// re-extracting on demand or persisting source.
interface ScannedFile {
  path: string
  suspicious: boolean
  findings?: string[]
}

const filesExpanded = ref(false)
const filesLoaded = ref(false)
const scanFiles = ref<ScannedFile[]>([])
const scanFilesLoading = ref(false)
const scanFilesPass = ref<1 | 2>(1)
const suspiciousOnly = ref(false)
const scanFilesMeta = ref<{
  total: number
  has_more: boolean
  suspicious_count: number
  offset: number
}>({ total: 0, has_more: false, suspicious_count: 0, offset: 0 })

async function loadScanFiles(offset: number) {
  if (!report.value?.server_name) return
  scanFilesLoading.value = true
  try {
    const response = await api.getScanFiles(
      report.value.server_name,
      100,
      offset,
      scanFilesPass.value,
      suspiciousOnly.value,
    )
    if (response.success && response.data) {
      const incoming = (response.data.files as ScannedFile[]) || []
      scanFiles.value = offset === 0 ? incoming : [...scanFiles.value, ...incoming]
      scanFilesMeta.value = {
        total: response.data.total_files || 0,
        has_more: response.data.has_more || false,
        suspicious_count: response.data.suspicious_count || 0,
        offset: offset + incoming.length,
      }
      filesLoaded.value = true
    }
  } catch {
    // Silently fail — the rest of the report is still useful and the user
    // can retry by toggling the disclosure or switching the pass selector.
  } finally {
    scanFilesLoading.value = false
  }
}

async function onScannedFilesToggle(event: Event) {
  const checkbox = event.target as HTMLInputElement
  filesExpanded.value = checkbox.checked
  if (checkbox.checked && !filesLoaded.value) {
    await loadScanFiles(0)
  }
}

async function loadMoreFiles() {
  await loadScanFiles(scanFilesMeta.value.offset)
}

async function switchFilesPass(pass: 1 | 2) {
  if (scanFilesPass.value === pass) return
  scanFilesPass.value = pass
  scanFiles.value = []
  filesLoaded.value = false
  await loadScanFiles(0)
}

async function onSuspiciousFilterChange() {
  // Reset list and reload when the filter toggles. The backend applies the
  // filter server-side so we don't accidentally hide a "suspicious_count"
  // value from the unfiltered totals.
  scanFiles.value = []
  filesLoaded.value = false
  await loadScanFiles(0)
}

// Heuristic: trim long finding titles so they fit in the inline chip
// without overflowing. The full title is preserved in the chip's title
// attribute (browser-native tooltip).
function truncateFindingTitle(title: string): string {
  if (!title) return ''
  if (title.length <= 28) return title
  return title.slice(0, 26) + '…'
}

function formatFileSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
}

onMounted(async () => {
  await loadReport()
  await loadServerStatus()
})
</script>
