# ---------- Clients: plain table, Manage opens a side panel
def clients_body():
    return h1('Clients','3 connected',btn('Connect client','p')+btn('Add other client')+btn('Move clients'))+tabs(['Clients','Tokens','Endpoint'],'Clients')+\
     note('review',b('1 client still uses the admin key.'),mut('Give it its own key so you can limit and revoke it.'),sbtn('Upgrade'))+\
     table(['Client','State','Can reach','Last seen',''],[
      [b('Claude Code'),tag('ok','Connected'),'All servers',mut('2m ago'),sbtn('Manage')],
      [b('Cursor'),tag('ok','Connected'),'Profile “read-only”, locked',mut('9m ago'),sbtn('Manage')],
      [b('VS Code')+' '+small('admin key'),tag('review','Connected, not verified'),'All servers',mut('1h ago'),sbtn('Manage')],
      [b('Claude Desktop'),tag('off','Points to another MCPProxy'),mut('Unknown'),mut('never'),sbtn('Point here')],
      [b('ci-agent')+' '+small('custom'),tag('off','Seen 4d ago'),'Profile “work”',mut('4d ago'),sbtn('Manage')],
      [b('Windsurf'),tag('off','Not connected'),mut('—'),mut('never'),sbtn('Connect','p')]])+\
     f'<details style="{BOX}"><summary style="padding: 12px 16px; {T14}; font-weight: 600; cursor: pointer">5 more apps are not installed on this computer</summary><div style="padding: 0 16px 12px; {T14}; color: var(--muted)">Gemini CLI, Codex, Kiro, Zed, Goose</div></details>'+\
     box(pad(row(b('Another client?'),mut('Copy this into its MCP settings:'),code('{"url": "http://127.0.0.1:8080/mcp"}'),sbtn('Copy config'))))
P['Clients.dc.html']=shell('Clients','Clients',clients_body())
hr='<hr style="border: 0; border-top: 1px solid var(--border); margin: 0">'
def psec(t,*c): return col(f'<h3 style="margin: 0; font-size: 12px; line-height: 16px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: var(--muted)">{t}</h3>',*c,gap=10)
def line(l,v,a=''): return f'<div style="display: flex; align-items: center; gap: 12px; min-height: 32px"><span style="flex: 0 0 110px; {T14}; color: var(--muted)">{l}</span><span style="flex: 1; min-width: 0; {T14}">{v}</span>{a}</div>'
panel=f'<aside aria-label="Manage Claude Code" style="position: absolute; top: 0; right: 0; bottom: 0; width: 480px; max-width: 100%; box-sizing: border-box; background: var(--base-100); border-left: 1px solid var(--border); box-shadow: -8px 0 24px rgba(0,0,0,0.12); padding: 20px; display: flex; flex-direction: column; gap: 16px; overflow: auto">'+\
 row(h2('Claude Code'),tag('ok','Connected'),f'<span style="margin-left: auto">{sbtn("Close")}</span>')+\
 mut('Last seen 2m ago · 3 sessions')+\
 row(link('Activity','Activity.dc.html'),link('Sessions','#'),link('Tools it sees','Tools.dc.html'),link('Usage','Usage.dc.html'),gap=16)+hr+\
 psec('What it can use',line('Profile',sel('Profile',['All servers','read-only','work'],200)),line('Lock',f'<label style="display: flex; align-items: center; gap: 8px">{check("Lock profile")} Agent cannot switch profile</label>'),row(link('Explain what this client can reach','#')))+hr+\
 psec('How it connects',line('Config file',code('~/.claude.json'),sbtn('Copy path')),line('Sign-in','API key',sbtn('Use browser sign-in')),row(sbtn('Check connection'),mut('Last change: connected 12 Nov'),link('Undo','#')))+hr+\
 psec('Key',line('Key',code('mcp_agt_3c…')+' '+mut('created 12 Nov'),sbtn('Rotate')),mut('Rotating gives a new key and keeps the old one working for 24 hours.'))+hr+\
 f'<details><summary style="{T14}; font-weight: 600; cursor: pointer">Disconnect or remove</summary><div style="display: flex; flex-direction: column; gap: 10px; padding-top: 12px">'+\
 line('Disconnect',mut('Restores the config file. The key stays valid.'),sbtn('Disconnect…'))+line('Revoke key',mut('The client stops working at once.'),sbtn('Revoke…','d'))+line('Forget',mut('Removes this client and its history link.'),sbtn('Forget…','d'))+'</div></details></aside>'
P['ClientManage.dc.html']=shell('Clients, manage a client','Clients',clients_body(),overlay=panel)

# ---------- Usage with its four charts
import random
random.seed(7)
def chartbox(t,sub,inner,x=''): return f'<div style="flex: 1 1 420px; min-width: 0; {BOX}; padding: 16px; display: flex; flex-direction: column; gap: 12px{x}">{row(b(t),mut(sub))}{inner}</div>'
def hbar(rows,tone='brand',unit=''):
    return col(*[f'<div style="display: flex; align-items: center; gap: 12px"><span style="flex: 0 0 190px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{code(n)}</span><span style="flex: 1; height: 10px; background: var(--base-200); border-radius: 4px"><span style="display: block; width: {p}%; height: 10px; border-radius: 4px; background: var(--{tone})"></span></span><span style="flex: 0 0 64px; text-align: right; font-size: 13px; font-variant-numeric: tabular-nums; color: var(--muted)">{v}{unit}</span></div>' for n,p,v in rows],gap=8)
def timeline():
    pts=' '.join(f'{i*20},{random.randint(15,105)}' for i in range(24))
    return f'<svg role="img" aria-label="Calls over time by server" viewBox="0 0 460 120" preserveAspectRatio="none" style="width: 100%; height: 150px; border-bottom: 1px solid var(--border)"><polyline points="{pts}" style="fill: none; stroke: var(--brand); stroke-width: 2"></polyline><polyline points="0,110 60,104 120,108 180,90 240,101 300,70 360,96 460,100" style="fill: none; stroke: var(--status-error); stroke-width: 2"></polyline></svg>'+row(tag('busy','Calls'),tag('error','Errors'))
P['Usage.dc.html']=shell('Usage','Usage',
 h1('Usage','Updated 20 s ago',btn('Refresh')+btn('Export'))+
 row(f'<span style="display: inline-flex">{chip("1 hour")}{chip("24 hours",True)}{chip("7 days")}{chip("30 days")}{chip("All time")}</span>',sel('Filter calls by status',['All calls','Succeeded','Failed','Blocked'],150),sel('Sort tools by',['Most calls','Most errors','Largest responses'],170),menu('View as'))+
 box(row(stat('about 91%','tokens saved, estimate'),stat('55','calls, 9 active tools'),stat('9','errors','error'),stat('4','blocked','review'),gap=0))+
 row(chartbox('Calls per tool','click a bar to open those calls',hbar([('github:search_issues',100,31),('datadog:query_metrics',29,9),('aws-docs:search',26,8),('brave-search:web_search',23,7)])),
     chartbox('Error rate','share of failed calls per tool',hbar([('datadog:query_metrics',100,100),('aws-docs:search',13,13),('github:search_issues',0,0)],'status-error','%')),gap=16,x='; align-items: stretch')+
 row(chartbox('Response size','largest tool responses',hbar([('github:get_file',100,'48 KB'),('aws-docs:search',61,'29 KB'),('brave-search:web_search',22,'11 KB')])),
     chartbox('Timeline','calls and errors over the period',timeline()),gap=16,x='; align-items: stretch')+
 mut('All four charts, the time window, the status filter, sorting and the token-savings tiles stay as they are today. Only colours, type and spacing change.'))
