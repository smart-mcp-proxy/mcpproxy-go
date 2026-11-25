import re
OLD=os.path.join(os.path.dirname(os.path.abspath(__file__)),'..','old')
# ---------- Servers (light + dark)
def srow(n,tr,p,r,t,l,a,k=''):
    act=row((sbtn(a,k) if a else ''),more(),gap=6,x='; justify-content: flex-end; flex-wrap: nowrap')
    return [link(n,'ServerDetail.dc.html','; font-weight: 600; color: var(--base-content)')+'<br>'+f'<span style="font-family: var(--font-mono); font-size: 12px; line-height: 16px; color: var(--muted)">{tr}</span>',p,f'<span style="display: block; max-width: 340px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted)">{r}</span>',t,f'<span style="white-space: nowrap">{l}</span>',act]
disc=pad(row(col(kv([('Id',code('datadog')),('Transport','stdio, not connected'),('Trust mode','Scan (default)'),('Last scan','Clean, 3 days ago'),('Tools','0 live, 6 configured, 0 to review')]),x='; flex: 1 1 280px'),
  col(b('Error'),f'<pre style="margin: 0; padding: 10px; background: var(--base-100); border: 1px solid var(--border); border-radius: 4px; font-family: var(--font-mono); font-size: 12px; line-height: 18px; white-space: pre-wrap">exec: "datadog-mcp": executable file not found in $PATH</pre>',row(sbtn('Copy error'),link('Open logs','#','; font-size: 13px'),link('Open server','ServerDetail.dc.html','; font-size: 13px')),small('More menu: Restart · Disable · Scan · Logs · Edit · Trust mode · Remove'),x='; flex: 2 1 360px; min-width: 0',gap=8),gap=24,x='; align-items: flex-start'),x='; background: var(--base-200)')
servers=h1('Servers','34 servers, 33 enabled, 56 tools available',btn('Refresh')+btn('Scan all'))+\
 row(chip('All 34',True),chip('Not online 6'),chip('Online 28'),chip('Disabled 1'),menu('More filters'),menu('View as'),f'<span style="margin-left: auto">{inp("Filter by name")}</span>')+\
 table(['Server','Status','Reason','>Tools','>Last call',''],[
  srow('datadog','stdio',ERR,'Command not found: datadog-mcp','0','2m ago · '+link('9 errors','Activity.dc.html','; font-size: 13px; color: var(--status-error)'),'Fix config'),
  srow('legacy-tools','stdio',SEC,'Missing API_TOKEN','0','never','Add secret'),
  srow('internal-api','http',SIGN,'Sign-in expired 2 days ago','0','2d ago','Sign in'),
  srow('community-weather','stdio',REV,'New server, 2 tools, scan clean','2','never','Review','p'),
  srow('huggingface','stdio',REV,'1 tool changed its description','12','1h ago','Review','p'),
  srow('pagerduty','http',CONN,'Connection refused, next try in 30 s','0','3h ago','Restart'),
  srow('aws-docs','stdio',ON,'','4','2m ago',''),
  srow('brave-search','stdio',ON,'','2','14m ago',''),
  srow('calendar-with-a-very-long-descriptive-server-name','http',ON,'','9','1h ago',''),
  srow('github','http',ON,'','41','2m ago',''),
  srow('elevenlabs','stdio',OFF,'Turned off by you','0','3d ago','Enable')],minw=820)+\
 row(mut('Showing 11 of 34. Servers that are not online are listed first.'),f'<span style="margin-left: auto">{link("Command-line hints","#")}</span>')
P['Main.dc.html']=shell('Servers','Servers',servers)
dark=open(os.path.join(OLD,'ServersDark.dc.html')).read()
m=re.search(r':root\{[^}]*\}',dark)
dv=m.group(0)
for k,v in (('--status-off','oklch(80% 0 0)'),('--status-off-soft','oklch(32% 0 0)')):
    if k+':' not in dv: dv=dv[:-1]+f';{k}:{v}'+'}'
light=re.search(r':root\{[^}]*\}',ROOT).group(0)
P['ServersDark.dc.html']=P['Main.dc.html'].replace(light,dv).replace('<title>Servers</title>','<title>Servers, dark</title>')
missing=[k for k in re.findall(r'--[a-z0-9-]+(?=:)',light) if k+':' not in dv]
print('dark missing vars:',missing)

# ---------- Settings: keep the form, new shell
s=open(os.path.join(OLD,'Settings.dc.html')).read()
a=s.index('<h1 style="margin: 0; font-size: 30px'); z=s.index('    </div>\n  </main>')
body=s[a:z]
body=body.replace('>Settings</h1>','>Settings</h1>'+row(inp('Search all settings',280),btn('Reload from file'),link('Full reference','#')))
body=body.replace('>Save and restart</button>','>Save General</button>').replace('1 unsaved change. Restarting disconnects agents for a few seconds.','1 unsaved change in General. It needs a restart: after saving, this page shows how.')
body=body.replace('Where MCPProxy listens. Keep 127.0.0.1 to stay on this computer. <a href="#">Learn more</a>','Where MCPProxy listens. Keep 127.0.0.1 to stay on this computer. <a href="#">Show full help</a>')
assert 'Save General' in body
P['Settings.dc.html']=shell('Settings','Settings',body,maxw='1040px')

# ---------- phones: words, header, details, more
def phone(name):
    t=open(os.path.join(OLD,name)).read()
    t=t.replace('Needs attention 6</button>','Not online 6</button>').replace('All 35</button>','All 34</button>').replace('Connected 28</button>','Online 28</button>').replace('Reconnecting</span>','Connecting</span>')
    t=t.replace('>Edit command</button>','>Fix config</button>').replace('>Edit</button>','>Fix config</button>').replace('>Retry now</button>','>Restart</button>')
    t=re.sub(r'Error</span>(\s*</div>\s*<span[^>]*>Sign-in expired)',r'Sign-in required</span>\1',t)
    t=t.replace('>Error</span> <span style="color: var(--muted)">Sign-in expired','>Sign-in required</span> <span style="color: var(--muted)">Expired 2 days ago')
    hb='<a href="ServersPhone.dc.html" aria-label="28 of 34 servers online" style="margin-left: auto; min-height: 44px; display: flex; align-items: center; padding: 0 6px; font-size: 14px; font-weight: 600; color: var(--base-content); text-decoration: none">28/34</a><button type="button" aria-label="6 need attention" style="min-width: 44px; height: 44px; border: 0; border-radius: 4px; background: var(--status-error-soft); color: var(--status-error); font-size: 14px; font-weight: 700">6</button>\n    <button type="button" aria-label="Search" style="width: 44px;'
    old='<button type="button" aria-label="Search" style="margin-left: auto; width: 44px;'
    assert old in t; t=t.replace(old,hb)
    return t
sp=phone('ServersPhone.dc.html')
mb='<button type="button" aria-label="More actions" style="flex: none; width: 44px; height: 44px; border: 1px solid var(--border); border-radius: 4px; background: var(--base-100); color: var(--base-content); font-size: 16px; font-weight: 700">⋯</button>'
sp,n1=re.subn(r'(<button type="button" style="align-self: flex-start;[^>]*>[^<]*</button>|<a href="Review.dc.html" style="align-self: flex-start;[^>]*>[^<]*</a>)',lambda m:'<div style="display: flex; align-items: center; gap: 8px">'+m.group(1)+'<span style="margin-left: auto"></span>'+mb+'</div>',sp)
print('phone rows patched:',n1)
P['ServersPhone.dc.html']=sp
P['HomePhone.dc.html']=phone('HomePhone.dc.html')
