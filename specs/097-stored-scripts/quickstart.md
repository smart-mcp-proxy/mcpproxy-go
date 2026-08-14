# Quickstart: stored scripts

```bash
mkdir -p ~/.mcpproxy/scripts
cat > ~/.mcpproxy/scripts/fetch-prs.js <<'JS'
var rs = call_tools([1,2,3].map(function(n){
  return {server:"github", tool:"get_pull_request",
          args:{owner:input.owner, repo:input.repo, pullNumber:n}};
}));
({titles: rs.map(function(r){ return r.ok ? JSON.parse(r.result.content[0].text).title : "ERR"; })})
JS

mcpproxy code scripts list
mcpproxy code exec --script fetch-prs --input '{"owner":"acme","repo":"api"}'
```

MCP clients call code_execution with `{"script": "fetch-prs", "input": {...}}` instead of `code` —
the 19KB workflow costs a name per run. Unknown name? The error lists what exists.
Edit by atomic replace (write temp + rename) and the next run picks it up — no restart.
