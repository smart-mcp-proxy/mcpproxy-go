# ---------- Skills over MCP
SK={'ok':tag('ok','Approved'),'pend':tag('review','Awaiting review'),'chg':tag('review','Changed'),'blk':tag('error','Blocked'),'un':tag('off','Unusable')}
def sdhead(tab,st=REV,reason='2 skills awaiting review',act='Review skills'):
    return row(link('Servers','Main.dc.html'),mut('/'),mut('huggingface'))+\
     box(pad(row(f'<h1 style="margin: 0; font-size: 30px; line-height: 36px; font-weight: 700">huggingface</h1>',st,f'<span style="margin-left: auto; display: flex; gap: 8px">{btn(act,"p")}{menu("Actions")}</span>',gap=12),f'<p style="margin: 8px 0 0; {T14}; color: var(--muted)">{reason}. 12 tools online.</p>'))+\
     tabs(['Tools 12','Review and scans','Skills 5','Logs','Configuration'],tab)
P['Skills.dc.html']=shell('Server detail, skills','Servers',
 sdhead('Skills 5')+
 row(chip('All skills 5'),chip('Needs review 2',True),mut('Last refreshed 2m ago'),f'<span style="margin-left: auto; display: flex; gap: 8px">{btn("Refresh")}</span>')+
 table(['Skill','What it is for','State','Scan','>Files',''],[
  [link('hf-model-card','SkillCard.dc.html','; font-weight: 600'),mut('Write a model card from a repository.'),SK['pend'],tag('review','2 warnings'),'4',sbtn('Review','p')],
  [link('hf-dataset-upload','SkillCard.dc.html','; font-weight: 600'),mut('Upload and version a dataset.'),SK['chg'],tag('ok','Clean'),'6',sbtn('Review changes','p')],
  [link('hf-spaces-deploy','#','; font-weight: 600'),mut('Deploy a Space from a template.'),SK['ok'],tag('ok','Clean'),'3',sbtn('View')],
  [link('hf-eval-runner','#','; font-weight: 600'),mut('Run an evaluation job.'),SK['blk'],tag('error','Dangerous'),'5',sbtn('Unblock')],
  [link('hf-legacy-notes','#','; font-weight: 600'),mut('—'),SK['un']+' '+small('SKILL.md is missing'),mut('—'),'1',sbtn('View')]],minw=860)+
 row(mut('Showing 5 of 5.'),link('What are skills?','#'))+
 h2('Other states of this tab')+
 col(note('off',b('Skills over MCP is turned off.'),mut('Set enable_skills to true in the configuration to read skills from servers.'),link('Open settings','Settings.dc.html')),
     note('off',b('This server is excluded from agents.'),mut('expose_skills is false for this server. You can still read its skills here.'),link('Configuration','#')),
     note('off',b('This server does not offer skills.')),
     note('review',b('List truncated.'),mut('The server listed more skills than MCPProxy keeps (200).')),gap=8))

fl=lambda p,s,st='',a='View': f'<div style="display: flex; align-items: center; gap: 12px; padding: 8px 16px; border-bottom: 1px solid var(--border)">{code(p)}{st}<span style="margin-left: auto; font-size: 13px; color: var(--muted)">{s}</span>{sbtn(a)}</div>'
dl=lambda sign,t,tone: f'<div style="display: flex; gap: 8px; padding: 0 12px; background: var(--status-{tone}-soft); font-family: var(--font-mono); font-size: 12px; line-height: 20px"><span style="color: var(--status-{tone}); font-weight: 700">{sign}</span><span>{t}</span></div>'
P['SkillCard.dc.html']=shell('Skill review card','Servers',
 sdhead('Skills 5')+row(link('Back to skills','Skills.dc.html'))+
 box(pad(row(h2('hf-dataset-upload'),SK['chg'],small('version sk1-8f2c… · approved copy sk1-41ab…')),f'<p style="margin: 8px 0 0; {T14}">Upload and version a dataset on the Hub.</p>',
   f'<div style="margin-top: 12px">{row(btn("Read SKILL.md"),btn("View approved"),small("Agents keep getting the approved copy until you decide."))}</div>'))+
 note('review',b('Scan: 1 warning'),mut('2 of 2 text files scanned, complete.'),link('Show finding','#'))+
 box(pad(row(tag('review','Warning'),code('scripts/push.sh'),mut('line 14: sends a file to a host outside huggingface.co')),x='; padding: 10px 16px'))+
 h2('Changes since the approved copy',sbtn('Show line diff'))+
 box(fl('SKILL.md','2.1 KB',tag('review','Changed')),
     f'<div style="border-bottom: 1px solid var(--border); padding: 8px 0">{dl("−","Upload with `hf upload` and stop.","error")}{dl("+","Upload with `hf upload`, then run scripts/push.sh.","ok")}</div>',
     fl('scripts/push.sh','640 B',tag('ok','Added')),fl('templates/old.md','—',tag('error','Removed'),'View approved'),
     f'<details style="border-bottom: 1px solid var(--border)"><summary style="padding: 8px 16px; {T14}; font-weight: 600; cursor: pointer">Unchanged files (3)</summary></details>',
     f'<details><summary style="padding: 8px 16px; {T14}; font-weight: 600; cursor: pointer">File hashes</summary></details>')+
 row(btn('Approve changes','p'),btn('Block','d'),mut('Approving applies to exactly this version. If the server changes it again, you review again.')),maxw='1100px')

qr=lambda n,st,sub,acts,age: [link(n,'#','; font-weight: 600; color: var(--base-content)'),st,mut(sub),mut(age),row(*acts,gap=6,x='; justify-content: flex-end')]
P['ReviewQueue.dc.html']=shell('Review queue','Review',
 h1('Review','2 servers and 3 skills wait for you',btn('Refresh'))+
 row(chip('Active 3',True),chip('All 5'),inp('Search servers'),menu('Sort: oldest first'),f'<span style="margin-left: auto">{mut("Scanner rules v0.1.1, checked today")} {link("History","#")}</span>')+
 table(['Server','Status','What waits','Waiting',''],[
  qr('community-weather',REV,'New server, 2 tools, scan clean',[sbtn('Review','p')],'1h'),
  qr('huggingface',REV,'1 tool changed · 2 skills awaiting review (1 changed)',[sbtn('Review tools','p'),sbtn('Review skills')],'3h'),
  qr('notion',ON,'1 skill awaiting review',[sbtn('Review skills','p')],'1d'),
  qr('internal-api',SIGN,'New server. Sign in first, then review.',[sbtn('Sign in'),more()],'2d')],minw=860)+
 mut('A row with only skills opens the server\'s Skills tab. A row with tools and skills offers both. Tool review and skill review stay separate decisions.')+
 h2('Where else skills show up')+
 box(pad(kv([('Home, needs attention','“huggingface: 2 skills awaiting review” with the button Review skills'),('Sidebar','The Review count includes servers that have only skills to review'),('Activity','Rows of type “Skill Access” and “Skill Approval Change”, for administrators, with SVG icons'),('Server row','Reason “2 skills awaiting review”; skill counts in Details'),('Turning it on','enable_skills and per-server expose_skills are set in the config file today; the tab says so when either is off')]))))
