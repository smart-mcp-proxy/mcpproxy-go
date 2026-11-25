ERR=pill('error','Error'); REV=pill('review','Needs review'); ON=pill('ok','Online'); CONN=pill('busy','Connecting'); OFF=pill('off','Disabled'); SIGN=pill('error','Sign-in required'); SEC=pill('error','Secret required')
details=lambda: link('Details','#','; font-size: 13px')

# ---------- Server detail
P['ServerDetail.dc.html']=shell('Server detail','Servers',
 row(link('Servers','Main.dc.html'),mut('/'),mut('datadog'))+
 box(pad(row(f'<h1 style="margin: 0; font-size: 30px; line-height: 36px; font-weight: 700">datadog</h1>',ERR,f'<span style="margin-left: auto; display: flex; gap: 8px">{btn("Fix config","p")}{menu("Actions")}</span>',gap=12),
   f'<p style="margin: 8px 0 0; {T14}">Command not found: {code("datadog-mcp")}. MCPProxy could not start this server. {link("Details","#details")}</p>',
   f'<p style="margin: 4px 0 0">{small("Actions menu: Restart · Disable · Sign in again · Discover tools · Quarantine · Refresh · Remove")}</p>'))+
 tabs(['Tools 0','Review and scans','Skills','Logs','Configuration'],'Configuration')+
 row(col(box(pad(h2('What went wrong'),f'<p style="margin: 8px 0; {T14}">The command {code("datadog-mcp")} is not on the PATH MCPProxy uses.</p>',
     col(row(b('1.'),mut('Install it:'),code('npm i -g datadog-mcp'),sbtn('Copy')),row(b('2.'),mut('Or set the full path in the command field, then save.')),row(b('3.'),mut('Restart the server.'),sbtn('Restart')),gap=8),
     f'<details id="details" open style="margin-top: 12px"><summary style="{T14}; font-weight: 600; cursor: pointer">Details</summary><pre style="margin: 8px 0 0; padding: 12px; background: var(--base-200); border-radius: 4px; font-family: var(--font-mono); font-size: 12px; line-height: 18px; white-space: pre-wrap">exec: "datadog-mcp": executable file not found in $PATH\nlast attempt 2m ago · 14 reconnects · MCPX_UPSTREAM_SPAWN_FAILED</pre>{row(sbtn("Copy error"),link("Open logs","#","; font-size: 13px"),link("Docs for this error","#","; font-size: 13px"))}</details>')),
   box(pad(h2('Command',sbtn('Edit')),kv([('Transport','stdio'),('Command',code('datadog-mcp')),('Arguments',code('--site datadoghq.eu')),('Working directory',mut('not set')),('Environment',code('DD_API_KEY')+' '+mut('from keyring')+' '+sbtn('Reveal'))]))),x='; flex: 2 1 480px; min-width: 0'),
  col(box(pad(h2('State'),kv([('Id',code('datadog')),('Enabled','Yes'),('Transport','stdio, not connected'),('Reconnects','14'),('Tools','0 live, 6 configured, 0 to review'),('Tool list size','about 2,100 tokens'),('Last call','2m ago, 9 errors in 24 h '+link('See calls','Activity.dc.html'))]))),
      box(pad(h2('Trust',sbtn('Change')),kv([('Trust mode','Scan (default)'),('Last scan','Clean, 3 days ago'),('Isolation','Docker, node image')]))),x='; flex: 1 1 300px; min-width: 0'),gap=16,x='; align-items: flex-start'))

# ---------- Review (captured)
def tool(n,d,labels,on=True,diff=''):
    return f'<div style="display: flex; gap: 12px; padding: 12px 16px; border-bottom: 1px solid var(--border)">{check("Allow "+n,on)}<div style="flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px">{row(code(n),labels)}{mut(d)}{diff}{row(link("Schema","#","; font-size: 13px"),link("Full description","#","; font-size: 13px"))}</div></div>'
revhead=lambda cap: box(pad(row(f'<h1 style="margin: 0; font-size: 30px; line-height: 36px; font-weight: 700">community-weather</h1>',REV,tag('review','Quarantined'),gap=12),
   f'<div style="margin-top: 12px">'+kv([('Launch',code('npx -y community-weather-mcp')+' '+sbtn('Copy')),('Transport','stdio, Docker isolation'),('Added','Today 14:02 by you, from the catalog'),('Trust mode','Scan (default)')])+'</div>'))
P['Review.dc.html']=shell('Review a server','Review',
 row(link('Review queue','#'),mut('/'),mut('community-weather'))+revhead(True)+
 note('ok',b('Scan: clean'),mut('3 scanners, 2 tools, finished 14:03.'),link('Full report','#'),link('Scan again','#'))+
 row(inp('Search tools'),sel('Tier',['All tiers','Read','Write','Destructive','No annotations'],150),sel('State',['All states','New','Changed','Approved'],150),mut('Showing 2 of 2'),f'<span style="margin-left: auto">{sbtn("Select all filtered")} {sbtn("Block all filtered")}</span>')+
 box(tool('get_forecast','Returns the forecast for a city.',tag('review','New')),
     tool('set_home_city','Stores your home city on the server.',tag('review','New')+tag('review','Writes')),
     pad(row(btn('Approve server (2 of 2 tools)','p'),btn('Approve all (2 tools)'),btn('Reject','d'),mut('Unchecked tools are blocked. You can change this later on the Tools tab.')),x='; padding: 12px 16px'))+
 h2('Scans for this server',sbtn('Start scan'))+
 table(['When','Result','Scanners','Findings',''],[['Today 14:03',tag('ok','Clean'),'3 of 3','0',link('Report','#')],['Today 14:02',tag('busy','Skipped'),'Docker scanner unavailable','—',link('Why','#')]]))

# ---------- Review (not captured)
P['ReviewBlind.dc.html']=shell('Review before tools are known','Review',
 row(link('Review queue','#'),mut('/'),mut('acme-internal'))+
 box(pad(row(f'<h1 style="margin: 0; font-size: 30px; line-height: 36px; font-weight: 700">acme-internal</h1>',REV,tag('review','Quarantined'),gap=12),
   f'<div style="margin-top: 12px">'+kv([('Address',code('https://mcp.acme.example/mcp')+' '+sbtn('Copy')),('Transport','http'),('Added','Today 15:40 by you, pasted')])+'</div>'))+
 note('review',b('MCPProxy has not seen this server\'s tools yet.'),mut('Fetch them first so you can read what you approve.'))+
 box(pad(col(row(btn('Fetch tools to review','p'),mut('Connects to the server once, in quarantine. Nothing is approved.')),
   f'<hr style="border: 0; border-top: 1px solid var(--border); margin: 4px 0">',
   row(btn('Approve without seeing tools…'),btn('Reject','d'),mut('Asks you to confirm by naming the server.')),gap=12)))+
 box(pad(h2('If fetching does not work'),col(mut('Sign-in needed: the row shows Sign in first, then Review.'),mut('Server disabled: enable it, then fetch.'),mut('No tools returned: the page says so and stays here.'),gap=4)))
 ,maxw='1040px')

# ---------- Home
att=lambda n,p,r,a,k='': f'<div style="display: flex; align-items: center; gap: 12px; padding: 10px 16px; border-bottom: 1px solid var(--border)"><span style="flex: 0 0 200px">{link(n,"ServerDetail.dc.html","; font-weight: 600; color: var(--base-content)")}</span>{p}<span style="flex: 1; min-width: 0; {T14}; color: var(--muted)">{r}</span>{sbtn(a,k)}</div>'
act=lambda t,r,c,w: f'<div style="display: flex; align-items: center; gap: 12px; padding: 8px 16px; border-bottom: 1px solid var(--border)">{code(t)}{r}<span style="margin-left: auto; {T14}; color: var(--muted)">{c} · {w}</span></div>'
P['Home.dc.html']=shell('Home','Home',
 h1('Home','28 of 34 servers online')+
 note('busy',b('MCPProxy v0.76.0 is available.'),link('What changed','#'),sbtn('Update'),sbtn('Later'))+
 row(col(h2('Needs attention '+mut('6'),link('See all 6','#')),
   box(att('datadog',ERR,'Command not found: datadog-mcp','Fix config'),att('legacy-tools',SEC,'Missing API_TOKEN','Add secret'),att('community-weather',REV,'New server, 2 tools, scan clean','Review','p'),att('huggingface',REV,'1 tool changed its description','Review','p'),att('internal-api',SIGN,'Sign-in expired 2 days ago','Sign in')),
   h2('Recent activity',link('Open Activity','Activity.dc.html')),
   box(act('datadog:query_metrics',tag('error','Failed'),'Cursor','2m ago'),act('github:search_issues','','Claude Code','2m ago'),act('github:create_issue',tag('review','Blocked'),'Claude Code','9m ago'),act('aws-docs:search','','Claude Code','14m ago'),act('brave-search:web_search','','Cursor','31m ago')),x='; flex: 2 1 520px; min-width: 0',gap=8),
  col(h2('Last 24 hours',link('View usage','Usage.dc.html')),
   box(row(stat('55','calls'),stat('9','failed','error'),stat('4','blocked','review'),gap=0)),
   box(pad(kv([('Tools agents can use','56 on 28 servers'),('Routing mode','Retrieve tools '+link('Change','Settings.dc.html')),('Clients seen','Claude Code, Cursor '+link('Sessions','#')),('Last security scan','34 servers clean, 0 findings '+link('Open','Review.dc.html')),('Token savings','about 91% '+link('Breakdown','#'))]))),
   h2('Shortcuts'),row(btn('Connect clients'),btn('Import from client configs'),btn('Browse catalog'),btn('Recent sessions')),x='; flex: 1 1 340px; min-width: 0',gap=8),gap=24,x='; align-items: flex-start')+
 f'<details style="{BOX}"><summary style="padding: 12px 16px; {T14}; font-weight: 600; cursor: pointer">Topology: 2 clients, MCPProxy, 34 servers</summary><div style="padding: 0 16px 16px; {T14}; color: var(--muted)">The diagram with clients, proxy state and servers opens here, as it is today.</div></details>')

# ---------- Activity
ar=lambda t,tool,res,cl,d,x='': [mut(t),code(tool)+x,res,cl,mut(d),'<button type="button" aria-label="Open details" style="border: 0; background: none; color: var(--brand-text); font-size: 16px">›</button>']
ok='<span aria-label="Succeeded" style="color: var(--muted)">OK</span>'
actbody=lambda: h1('Activity','',btn('Export')+btn('Refresh'))+tabs(['Calls','Sessions','System','All'],'Calls')+box(row(stat('55','calls, 24 h'),stat('9','failed','error'),stat('4','blocked','review'),stat('2','with sensitive data','review'),gap=0))+row(fchip('Viewing as Cursor'),fchip('Last 24 hours'),fchip('Status: failed, blocked'),menu('Add filter'),menu('View as'),link('Clear all','#'),f'<span style="margin-left: auto; display: flex; align-items: center; gap: 12px"><label style="display: flex; align-items: center; gap: 6px; {T14}">{check("Group repeats",True)} Group repeats</label>{menu("Sort: newest first")}</span>')+table(['Time','Tool','Result','Client','>Duration',''],[
  ar('15:42:10','datadog:query_metrics',tag('error','Failed')+' '+mut('server not connected'),'Cursor','—',' '+sbtn('3 times ▸')),
  ar('15:33:02','github:create_issue',tag('review','Blocked')+' '+mut('needs write access ')+link('Why','#','; font-size: 13px'),'Cursor','2 ms'),
  ar('15:31:47','internal-api:list_orders',tag('error','Failed')+' '+mut('sign-in required'),'Cursor','18 ms'),
  ar('15:20:09','github:delete_branch',tag('review','Blocked')+' '+mut('destructive, profile rule ')+link('Why','#','; font-size: 13px'),'Cursor','1 ms'),
  ar('15:02:55','aws-docs:search',tag('error','Failed')+' '+mut('timeout after 60 s'),'Cursor','60.0 s'),
  ar('14:48:31','(no tool) quarantine change',tag('off','Recorded'),mut('you'),'—')])+row(mut('Showing 6 of 13'),f'<span style="margin-left: auto; display: flex; gap: 8px; align-items: center">{sel("Rows per page",["25 per page","50 per page","100 per page"],140)}{sbtn("Previous")}{sbtn("Next")}</span>')
P['Activity.dc.html']=shell('Activity','Activity',actbody())
drawer=f'<aside aria-label="Call details" style="position: absolute; top: 0; right: 0; bottom: 0; width: 520px; max-width: 100%; box-sizing: border-box; background: var(--base-100); border-left: 1px solid var(--border); box-shadow: -8px 0 24px rgba(0,0,0,0.12); padding: 20px; display: flex; flex-direction: column; gap: 16px; overflow: auto">'+row(h2('github:create_issue'),f'<span style="margin-left: auto">{sbtn("Close")}</span>')+row(tag('review','Blocked'),mut('15:33:02 today, 2 ms'))+note('review',b('Blocked: this client may read but not write.'),link('Why','#'),link('Open profile','ProfileEditor.dc.html'))+kv([('Type','Tool call'),('Server',link('github','ServerDetail.dc.html')),('Client','Cursor (reported), token '+code('mcp_agt_7f…')),('Scope','Profile “read-only”'),('Intent','write '+mut('“create a tracking issue”')),('Sensitive data','None found'),('Session',link('s_91ac…','#')),('Request id',code('req_01JD…')+' '+sbtn('Copy')),('Sizes','412 B in, 0 B out')])+h2('Arguments',sbtn('Copy'))+f'<pre style="margin: 0; padding: 12px; background: var(--base-200); border-radius: 4px; font-family: var(--font-mono); font-size: 12px; line-height: 18px; white-space: pre-wrap">{{\n  "repo": "acme/api",\n  "title": "Track flaky login test"\n}}</pre>'+row(link('All calls in this session','#'),link('All calls to this tool','#'))+'</aside>'
P['ActivityDetail.dc.html']=shell('Activity, call details','Activity',actbody(),overlay=drawer)

# ---------- Tools
tr=lambda n,d,l,u,on=False: f'<div style="display: flex; align-items: center; gap: 12px; padding: 8px 16px 8px 44px; border-top: 1px solid var(--border)">{check("Select "+n,on)}<span style="flex: 0 0 240px; min-width: 0">{code(n)}</span><span style="flex: 1; min-width: 0; {T14}; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis">{d}</span>{l}<span style="flex: 0 0 90px; text-align: right; font-size: 13px; color: var(--muted)">{u}</span>{details()}</div>'
sec=lambda n,p,c,rows,open_=True: f'<details{" open" if open_ else ""} style="{BOX}"><summary style="display: flex; align-items: center; gap: 12px; padding: 10px 16px; cursor: pointer">{b(n)}{p}{mut(c)}</summary>{rows}</details>'
P['Tools.dc.html']=shell('Tools','Tools',
 h1('Tools','12 of 56 tools, as Cursor sees them')+
 box(row(stat('12','Cursor can use'),stat('44','hidden from Cursor'),stat('0','disabled'),stat('1','need review','review'),gap=0))+
 row(inp('Search tools',260),sel('Server',['All servers','github','aws-docs']),chip('Only exceptions'),menu('More filters'),menu('View as'),fchip('Viewing as Cursor'),f'<span style="margin-left: auto; display: inline-flex">{chip("Grouped",True)}{chip("Flat list")}</span>')+
 row(small('More filters: Status · Tier · Approval · Callable. Flat list keeps sorting by name, server, tier, approval, enabled, usage, last used.'))+
 note('busy',b('3 selected'),mut('across 2 servers'),sbtn('Approve','p'),sbtn('Reject'),sbtn('Enable'),sbtn('Disable'),link('Clear','#'))+
 sec('github',ON,'41 tools, 2 selected',tr('search_issues','Search issues and pull requests.','','today')+tr('create_issue','Create an issue in a repository.',tag('review','Writes'),'today',True)+tr('delete_branch','Delete a branch.',tag('error','Destructive'),'3d ago',True)+tr('get_file','Read a file from a repository.','','today')+f'<div style="padding: 8px 16px 8px 44px; border-top: 1px solid var(--border)">{link("Show 37 more","#")}</div>')+
 sec('huggingface',REV,'12 tools, 1 selected',tr('model_search','Search models on the hub.',tag('review','Changed, needs review'),'1h ago',True)+tr('dataset_info','Describe a dataset.',tag('off','No annotations'),'never'))+
 sec('aws-docs',ON,'4 tools','',False)+sec('datadog',ERR,'0 live tools, 6 configured','',False)+
 row(mut('Sections 1–4 of 29'),f'<span style="margin-left: auto; display: flex; gap: 8px">{sel("Page size",["50 tools per page","100 tools per page"],170)}{sbtn("Previous")}{sbtn("Next")}</span>'))

# ---------- Clients
manage=pad(row(col(b('Connection'),kv([('Config file',code('~/.claude.json')+' '+sbtn('Copy path')),('Last seen','2m ago, 3 sessions '+link('Sessions','#')),('Sign-in','API key '+link('Switch to browser sign-in','#'))]),row(sbtn('Check connection'),sbtn('Disconnect…'),sbtn('Undo last change')),x='; flex: 1 1 300px',gap=8),
  col(b('Access'),kv([('Profile',sel('Profile',['All servers','read-only','work'],160)),('Locked',check('Lock profile',False)+' '+mut('agent cannot switch profile')),('Credential',code('mcp_agt_3c…')+' '+mut('created 12 Nov'))]),row(sbtn('Explain access'),sbtn('Rotate key'),sbtn('Revoke','d'),sbtn('Forget client','d')),x='; flex: 1 1 300px',gap=8),
  col(b('See what it did'),link('Activity','Activity.dc.html'),link('Sessions','#'),link('Tools it sees','Tools.dc.html'),link('Usage','Usage.dc.html'),x='; flex: 0 1 160px',gap=4),gap=24,x='; align-items: flex-start'),x='; background: var(--base-200)')
P['Clients.dc.html']=shell('Clients','Clients',
 h1('Clients','3 connected',btn('Connect client','p')+btn('Add other client')+btn('Move clients'))+tabs(['Clients','Tokens','Endpoint'],'Clients')+
 note('review',b('1 client still uses the admin key.'),mut('Give it its own key so you can limit and revoke it.'),sbtn('Upgrade'))+
 table(['Client','State','Can reach',''],[
  [b('Claude Code'),tag('ok','Connected'),'All servers',sbtn('Manage ▾')],manage,
  [b('Cursor'),tag('ok','Connected'),'Profile “read-only”, locked',sbtn('Manage ▸')],
  [b('VS Code')+' '+small('admin key'),tag('review','Connected, not verified'),'All servers',sbtn('Manage ▸')],
  [b('Claude Desktop'),tag('off','Points to another MCPProxy'),mut('Unknown'),sbtn('Point here')],
  [b('ci-agent')+' '+small('custom'),tag('off','Seen 4d ago'),'Profile “work”',sbtn('Manage ▸')],
  [b('Windsurf'),tag('off','Not connected'),mut('—'),sbtn('Connect','p')]])+
 f'<details style="{BOX}"><summary style="padding: 12px 16px; {T14}; font-weight: 600; cursor: pointer">5 more apps are not installed on this computer</summary><div style="padding: 0 16px 12px; {T14}; color: var(--muted)">Gemini CLI, Codex, Kiro, Zed, Goose</div></details>'+
 box(pad(row(b('Another client?'),mut('Copy this into its MCP settings:'),code('{"url": "http://127.0.0.1:8080/mcp"}'),sbtn('Copy config')))))

# ---------- Tokens
tk=lambda n,p,l,pr,e,u,s: [b(n)+'<br>'+code(p),l,pr,e,u,s,row(sbtn('Regenerate'),more())]
P['Tokens.dc.html']=shell('Clients, tokens','Clients',
 h1('Clients','',btn('Create token','p'))+tabs(['Clients','Tokens','Endpoint'],'Tokens')+
 table(['Token','Limits','Profile','Expires','Last used','State',''],[
  tk('ci-agent','mcp_agt_91…','2 servers, read only','work','in 23 days','4d ago',tag('ok','Active')),
  tk('cursor-laptop','mcp_agt_3c…',mut('Set by its profile'),'read-only','never','2m ago',tag('ok','Active')),
  tk('old-script','mcp_agt_0a…','Own server list, read and write '+link('Move to a profile','#','; font-size: 13px'),mut('none'),'in 2 days','31d ago',tag('review','Expires soon')),
  tk('contractor','mcp_agt_5e…','1 server, read only',mut('missing: “temp”'),'expired 3d ago','12d ago',tag('off','Expired'))],minw=900)+
 mut('More menu: Activity · Usage · Open client · Revoke · Delete. “Own server list” replaces the label “Legacy scope”.')+
 box(pad(h2('Create token'),row(inp('Name'),sel('Access',['Use a profile','Choose servers and permissions']),sel('Profile',['read-only','work']),sel('Expires',['30 days','90 days','Never']),btn('Create','p')),mut('The token is shown once after you create it.'))))

# ---------- Profiles
P['Profiles.dc.html']=shell('Profiles','Profiles',
 h1('Profiles','A profile is the set of servers and tools a client may use.',btn('Create profile','p'))+
 table(['Profile','Servers','Tool rules','Used by',''],[
  [link('read-only','ProfileEditor.dc.html','; font-weight: 600'),'12 of 34','Read tools only','Cursor, 1 token',row(sbtn('Edit'),sbtn('Try'),more())],
  [link('work','#','; font-weight: 600'),'8 of 34','3 tools denied','ci-agent',row(sbtn('Edit'),sbtn('Try'),more())],
  [link('everything','#','; font-weight: 600')+' '+tag('busy','Active default'),'All servers','No rules','Clients without a profile',row(sbtn('Edit'),sbtn('Try'),more())],
  [b('temp')+' '+tag('error','Missing'),mut('—'),mut('—'),'1 token '+link('Fix','Tokens.dc.html'),sbtn('Recreate')]])+
 mut('More menu: Rename · Duplicate · Set as default · Delete.'),maxw='1100px')

rule=lambda t,m,x: f'<div style="display: flex; align-items: center; gap: 12px; padding: 8px 16px; border-bottom: 1px solid var(--border)">{code(t)}<span style="margin-left: auto"></span>{sel("Rule for "+t,[m,"Allow","Deny","Ask in review"],150)}{sbtn("Remove")}</div>'
P['ProfileEditor.dc.html']=shell('Profile editor','Profiles',
 row(link('Profiles','Profiles.dc.html'),mut('/'),mut('read-only'))+h1('read-only','Used by Cursor and 1 token',btn('Try as this profile')+menu('More'))+
 box(pad(h2('Servers'),row(chip('github ×',True),chip('aws-docs ×',True),chip('brave-search ×',True),mut('and 9 more'),sbtn('Choose servers')),
   f'<div style="margin-top: 16px">{h2("What tools may do")}</div>',row(chip('Read',True),chip('Write'),chip('Destructive'),mut('Tools with no annotations count as write.')),
   f'<div style="margin-top: 16px">{h2("Longest tool response")}</div>',row(inp('characters',140,'8000'),mut('characters. Empty uses the global setting.'))))+
 h2('Rules for single tools',sbtn('Add rule'))+box(rule('github:create_issue','Allow',''),rule('github:delete_branch','Deny',''),rule('aws-docs:*','Allow',''))+
 row(btn('Save profile','p'),btn('Discard changes'),mut('Unsaved changes: 1 server, 1 rule.'))+
 mut('The save row sits after both the form and the rules and covers nothing.'),maxw='1040px')

# ---------- Secrets
P['Secrets.dc.html']=shell('Secrets','Secrets',
 h1('Secrets','Kept in your system keychain. Servers refer to them by name.',btn('Add secret','p')+btn('Refresh'))+
 note('error',b('1 secret is missing.'),mut('legacy-tools cannot start without API_TOKEN.'),sbtn('Add API_TOKEN','p'))+
 table(['Name','Where it is kept','Used by','State',''],[
  [code('API_TOKEN'),mut('—'),link('legacy-tools','ServerDetail.dc.html'),tag('error','Missing'),sbtn('Add','p')],
  [code('DD_API_KEY'),'Keychain','datadog',tag('ok','Set'),row(sbtn('Replace'),more())],
  [code('GITHUB_TOKEN'),'Environment variable','github',tag('ok','Set'),row(sbtn('Move to keychain'),more())],
  [code('OLD_KEY'),'Keychain',mut('no server'),tag('off','Not used'),row(sbtn('Delete','d'),more())]])+
 h2('Plain values found in your config')+
 table(['Server','Field','',''],[['brave-search',code('env.BRAVE_API_KEY'),mut('Stored as plain text in mcp_config.json'),sbtn('Move to keychain')]])+
 mut('More menu: Copy reference · Show servers · Delete.'),maxw='1100px')

# ---------- Usage
bar=lambda p: f'<span style="display: inline-block; width: 160px; height: 8px; background: var(--base-200); border-radius: 4px; vertical-align: middle"><span style="display: block; width: {p}%; height: 8px; background: var(--brand); border-radius: 4px"></span></span>'
P['Usage.dc.html']=shell('Usage','Usage',
 h1('Usage','',sel('Period',['Last 24 hours','Last 7 days','Last 30 days'],160)+menu('Viewing: all')+btn('Export'))+
 box(row(stat('55','calls'),stat('9','failed','error'),stat('4','blocked','review'),stat('91%','tokens saved'),stat('1.2 s','median response'),gap=0))+
 row(col(h2('By server'),table(['Server','>Calls','Share','>Failed','>Median'],[[link('github','#'),'31',bar(56),'0','0.8 s'],[link('datadog','#'),'9',bar(16),'9','—'],[link('aws-docs','#'),'8',bar(15),'1','2.1 s'],[link('brave-search','#'),'7',bar(13),'0','1.4 s']],minw=480),x='; flex: 2 1 480px; min-width: 0',gap=8),
  col(h2('By client'),table(['Client','>Calls','>Blocked'],[['Claude Code','38','1'],['Cursor','17','3']],minw=280),h2('Token savings',link('How this is measured','#')),box(pad(kv([('Tool list sent to agents','about 4,300 tokens'),('Without MCPProxy','about 48,000 tokens'),('Saved per request','about 91%, estimate')]))),x='; flex: 1 1 320px; min-width: 0',gap=8),gap=24,x='; align-items: flex-start')+
 h2('Least used tools',link('Open in Tools','Tools.dc.html'))+table(['Tool','>Calls, 30 days','Last used'],[[code('github:delete_branch'),'1','3d ago'],[code('huggingface:dataset_info'),'0','never']],minw=480))

# ---------- Add server
cat=lambda n,d,t,a='Add': f'<div style="flex: 1 1 280px; {BOX}; padding: 16px; display: flex; flex-direction: column; gap: 8px">{row(b(n),t)}{mut(d)}{row(sbtn(a,"p" if a=="Add" else ""),link("Details","#","; font-size: 13px"))}</div>'
P['AddServer.dc.html']=shell('Add server','Servers',
 row(link('Servers','Main.dc.html'),mut('/'),mut('Add server'))+h1('Add server','New servers start in quarantine until you review them.')+
 tabs(['Catalog','Paste config','Fill in a form','Import from clients'],'Catalog')+
 row(inp('Search the catalog, for example “postgres”',420),sel('Source',['All sources','Official registry','Docker MCP catalog'],200),mut('1,204 servers'))+
 row(cat('GitHub','Issues, pull requests, code search.',tag('ok','Official'),'Added'),cat('Postgres','Query a database, read-only by default.',tag('ok','Official')),cat('Sentry','Look up errors and releases.',tag('off','Community')),cat('Filesystem','Read and write files in folders you choose.',tag('ok','Official')),cat('Slack','Read channels and post messages.',tag('off','Community')),cat('Playwright','Drive a browser.',tag('ok','Official')),gap=16,x='; align-items: stretch')+
 row(sbtn('Previous'),sbtn('Next'),mut('Page 1 of 201'),f'<span style="margin-left: auto">{link("Manage catalog sources","Settings.dc.html")}</span>'))

# ---------- Setup wizard
step=lambda n,t,s: f'<button type="button" role="tab" aria-selected="{str(s=="now").lower()}" style="flex: 1 1 160px; display: flex; align-items: center; gap: 8px; padding: 10px 12px; border: 1px solid {"var(--brand)" if s=="now" else "var(--border)"}; border-radius: 4px; background: {"var(--brand-soft)" if s=="now" else "var(--base-100)"}; color: var(--base-content); {T14}; font-weight: 600; text-align: left"><span style="width: 22px; height: 22px; border-radius: 9999px; display: inline-flex; align-items: center; justify-content: center; font-size: 12px; background: {"var(--status-ok)" if s=="done" else "var(--brand)" if s=="now" else "var(--base-200)"}; color: {"var(--base-content)" if s=="todo" else "var(--on-brand)"}">{"✓" if s=="done" else n}</span>{t}</button>'
vs=lambda tone,t,d,cur=False: f'<div style="display: flex; gap: 12px; padding: 10px 16px; border-bottom: 1px solid var(--border){"; background: var(--brand-soft)" if cur else ""}">{tag(tone,t)}<span style="{T14}">{d}</span></div>'
P['Setup.dc.html']=shell('Setup','Setup',
 h1('Setup','Three steps. You can leave and come back.')+row(step(1,'Connect a client','done'),step(2,'Add servers','done'),step(3,'Check that it works','now'),gap=8)+
 box(pad(h2('Check that it works'),f'<p style="margin: 8px 0 12px; {T14}">Ask your agent something that needs a tool, for example:</p>',row(code('“Search GitHub issues in acme/api about login”'),sbtn('Copy'))),
   vs('busy','Now','Claude Code has connected, but no tool call has reached a server yet.',True))+
 h2('What this step can say')+box(vs('off','Unknown','MCPProxy cannot tell yet. Open your client and start a new chat.'),vs('off','No connection','No client has connected since MCPProxy started. Check step 1.'),vs('busy','Connected','A client connected. No tool call has reached a server yet.'),vs('ok','Working','A tool call reached a server and returned. Setup is done.'))+
 row(btn('Back'),btn('Finish','p'),link('Skip for now','Home.dc.html')),maxw='900px')

# ---------- Command palette
pr=lambda k,t,d,cur=False: f'<div role="option" aria-selected="{str(cur).lower()}" style="display: flex; align-items: center; gap: 12px; padding: 8px 16px{"; background: var(--brand-soft)" if cur else ""}"><span style="flex: 0 0 72px; font-size: 12px; line-height: 16px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: var(--muted)">{k}</span><span style="{T14}; font-weight: 600">{t}</span><span style="margin-left: auto; {T14}; color: var(--muted)">{d}</span></div>'
pal=f'<div style="position: absolute; inset: 0; background: rgba(0,0,0,0.35); display: flex; justify-content: center; align-items: flex-start; padding-top: 96px"><div role="dialog" aria-label="Search" style="width: 640px; max-width: calc(100% - 32px); {BOX}; box-shadow: 0 16px 48px rgba(0,0,0,0.25); overflow: hidden"><div style="display: flex; align-items: center; gap: 12px; padding: 12px 16px; border-bottom: 1px solid var(--border)"><input type="text" aria-label="Search" value="git" style="flex: 1; border: 0; outline: 0; background: none; color: var(--base-content); font-family: inherit; font-size: 16px; line-height: 24px">{small("Esc to close")}</div>'+pr('Server','github','Online, 41 tools',True)+pr('Tool','github:search_issues','Search issues and pull requests')+pr('Tool','github:create_issue','Writes')+pr('Setting','Catalog sources','Settings')+pr('Action','Add server','')+pr('Action','Connect client','')+pr('Action','Create token','')+pr('Page','Activity for github','')+f'<div style="display: flex; gap: 16px; padding: 8px 16px; border-top: 1px solid var(--border)">{small("↑ ↓ to move")}{small("Enter to open")}{small("Opens with ⌘K or /, never with Tab")}</div></div></div>'
P['Palette.dc.html']=shell('Search','Servers',h1('Servers','34 servers, 56 tools')+mut('The page behind the search dialog.'),overlay=pal)
