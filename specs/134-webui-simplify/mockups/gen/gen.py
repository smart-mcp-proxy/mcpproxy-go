import json,re,os,sys
D=sys.argv[1]
LOGO='/_blob/a2734d90f186b038360c927d1ecb2598'
ROOT=''':root{--base-100:oklch(100% 0 0);--base-200:oklch(93% 0 0);--base-content:oklch(22.389% 0.031 278.072);--muted:oklch(46% 0.031 278.072);--border:oklch(86% 0 0);--brand:oklch(55% 0.158 241.966);--on-brand:oklch(100% 0 0);--brand-text:oklch(42% 0.158 241.966);--brand-soft:oklch(95% 0.03 241.966);--status-ok:oklch(39% 0.194 149.214);--status-ok-soft:oklch(95% 0.05 149.214);--status-review:oklch(42% 0.14 75);--status-review-soft:oklch(95% 0.07 91.936);--status-error:oklch(46% 0.191 22.216);--status-error-soft:oklch(95% 0.035 22.216);--status-busy:oklch(41% 0.126 221.723);--status-busy-soft:oklch(95% 0.03 221.723);--status-off:oklch(40% 0 0);--status-off-soft:oklch(92% 0 0);--font-sans:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;--font-mono:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
body{margin:0;font-family:var(--font-sans);color:var(--base-content);background:var(--base-200)}
a{color:var(--brand-text)}a:hover{color:var(--base-content)}
button{font-family:inherit;cursor:pointer}
button:focus-visible,a:focus-visible,input:focus-visible,select:focus-visible{outline:2px solid var(--brand-text);outline-offset:2px}'''
T14='font-size: 14px; line-height: 20px'
BOX='background: var(--base-100); border: 1px solid var(--border); border-radius: 4px'
def mut(t,x=''): return f'<span style="{T14}; color: var(--muted){x}">{t}</span>'
def small(t): return f'<span style="font-size: 12px; line-height: 16px; color: var(--muted)">{t}</span>'
def code(t): return f'<code style="font-family: var(--font-mono); font-size: 13px; line-height: 20px">{t}</code>'
def btn(t,kind='',x=''):
    s={'p':'border: 1px solid var(--brand); background: var(--brand); color: var(--on-brand)','d':'border: 1px solid var(--status-error); background: var(--base-100); color: var(--status-error)','g':'border: 1px solid transparent; background: none; color: var(--brand-text)'}.get(kind,'border: 1px solid var(--border); background: var(--base-100); color: var(--base-content)')
    return f'<button type="button" style="padding: 6px 12px; border-radius: 4px; {s}; font-size: 14px; line-height: 20px; font-weight: 600; white-space: nowrap{x}">{t}</button>'
def sbtn(t,kind=''): return btn(t,kind,'; padding: 3px 10px; font-size: 13px')
def menu(t): return btn(t+' <span aria-hidden="true">▾</span>')
def more(): return '<button type="button" aria-label="More actions" style="padding: 3px 8px; border: 1px solid var(--border); border-radius: 4px; background: var(--base-100); color: var(--base-content); font-size: 13px; line-height: 20px; font-weight: 700">⋯</button>'
def link(t,h='#',x=''): return f'<a href="{h}" style="{T14}{x}">{t}</a>'
def pill(tone,t): return f'<span style="display: inline-flex; align-items: center; gap: 6px; padding: 2px 10px 2px 8px; border-radius: 4px; font-size: 13px; line-height: 20px; font-weight: 600; white-space: nowrap; color: var(--status-{tone}); background: var(--status-{tone}-soft)"><span style="width: 8px; height: 8px; border-radius: 9999px; background: currentColor"></span>{t}</span>'
def tag(tone,t): return f'<span style="padding: 1px 8px; border-radius: 4px; font-size: 12px; line-height: 18px; font-weight: 600; white-space: nowrap; color: var(--status-{tone}); background: var(--status-{tone}-soft)">{t}</span>'
def chip(t,on=False,x=''):
    s='border: 1px solid var(--brand); background: var(--brand); color: var(--on-brand)' if on else 'border: 1px solid var(--border); background: var(--base-100); color: var(--base-content)'
    return f'<button type="button" aria-pressed="{str(on).lower()}" style="padding: 5px 12px; border-radius: 4px; {s}; font-size: 13px; line-height: 20px; font-weight: 600; white-space: nowrap">{t}{x}</button>'
def fchip(t): return f'<span style="display: inline-flex; align-items: center; gap: 6px; padding: 3px 4px 3px 10px; border-radius: 4px; background: var(--brand-soft); color: var(--brand-text); font-size: 13px; line-height: 20px; font-weight: 600">{t}<button type="button" aria-label="Remove filter {t}" style="border: 0; background: none; color: inherit; font-size: 14px; padding: 0 6px">×</button></span>'
def inp(ph,w=220,val=''): return f'<input type="text" aria-label="{ph}" placeholder="{ph}" value="{val}" style="flex: 0 1 {w}px; min-width: 0; padding: 6px 10px; border: 1px solid var(--border); border-radius: 4px; background: var(--base-100); color: var(--base-content); font-family: inherit; {T14}">'
def sel(label,opts,w=180): return f'<select aria-label="{label}" style="flex: 0 1 {w}px; padding: 6px 10px; border: 1px solid var(--border); border-radius: 4px; background: var(--base-100); color: var(--base-content); font-family: inherit; {T14}">'+''.join(f'<option>{o}</option>' for o in opts)+'</select>'
def row(*c,gap=8,x=''): return f'<div style="display: flex; flex-wrap: wrap; align-items: center; gap: {gap}px{x}">'+''.join(c)+'</div>'
def col(*c,gap=16,x=''): return f'<div style="display: flex; flex-direction: column; gap: {gap}px{x}">'+''.join(c)+'</div>'
def box(*c,x=''): return f'<div style="{BOX}{x}">'+''.join(c)+'</div>'
def pad(*c,x=''): return f'<div style="padding: 16px{x}">'+''.join(c)+'</div>'
def h1(t,sub='',right=''): return row(f'<h1 style="margin: 0; font-size: 30px; line-height: 36px; font-weight: 700">{t}</h1>',mut(sub),f'<span style="margin-left: auto; display: flex; flex-wrap: wrap; gap: 8px">{right}</span>' if right else '',gap=12,x='; align-items: baseline')
def h2(t,right=''): return row(f'<h2 style="margin: 0; font-size: 18px; line-height: 28px; font-weight: 600">{t}</h2>',f'<span style="margin-left: auto; display: flex; flex-wrap: wrap; align-items: center; gap: 8px">{right}</span>' if right else '')
def tabs(items,active): return '<div role="tablist" style="display: flex; flex-wrap: wrap; gap: 4px; border-bottom: 1px solid var(--border)">'+''.join(f'<button type="button" role="tab" aria-selected="{str(i==active).lower()}" style="padding: 8px 14px; border: 0; border-bottom: 2px solid {"var(--brand)" if i==active else "transparent"}; background: none; color: {"var(--brand-text)" if i==active else "var(--base-content)"}; {T14}; font-weight: 600">{i}</button>' for i in items)+'</div>'
def table(heads,rows,minw=720):
    th=''.join(f'<th scope="col" style="text-align: {"right" if h.startswith(">") else "left"}; padding: 10px 16px; border-bottom: 1px solid var(--border); font-size: 12px; line-height: 16px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: var(--muted); white-space: nowrap">{h.lstrip(">")}</th>' for h in heads)
    out=[]
    for ri,r in enumerate(rows):
        if isinstance(r,str): out.append(f'<tr><td colspan="{len(heads)}" style="padding: 0; border-bottom: 1px solid var(--border)">{r}</td></tr>'); continue
        bb='' if ri==len(rows)-1 else '; border-bottom: 1px solid var(--border)'
        out.append('<tr>'+''.join(f'<td style="padding: 10px 16px{bb}; {T14}; text-align: {"right" if heads[i].startswith(">") else "left"}; vertical-align: middle">{c}</td>' for i,c in enumerate(r))+'</tr>')
    return f'<div style="overflow-x: auto; {BOX}"><table style="width: 100%; min-width: {minw}px; border-collapse: collapse"><thead><tr>{th}</tr></thead><tbody>'+''.join(out)+'</tbody></table></div>'
def kv(pairs): return '<dl style="margin: 0; display: grid; grid-template-columns: max-content 1fr; gap: 6px 24px">'+''.join(f'<dt style="{T14}; color: var(--muted)">{k}</dt><dd style="margin: 0; {T14}">{v}</dd>' for k,v in pairs)+'</dl>'
def b(t): return f'<span style="{T14}; font-weight: 600">{t}</span>'
def stat(n,l,tone=''): return f'<a href="#" style="flex: 1 1 140px; display: flex; flex-direction: column; padding: 12px 16px; color: var(--base-content); text-decoration: none; border-right: 1px solid var(--border)"><span style="font-size: 24px; line-height: 32px; font-weight: 700; font-variant-numeric: tabular-nums{("; color: var(--status-"+tone+")") if tone else ""}">{n}</span><span style="font-size: 13px; line-height: 16px; color: var(--muted)">{l}</span></a>'
def check(label='',on=False): return f'<input type="checkbox" aria-label="{label}"{" checked" if on else ""} style="width: 16px; height: 16px; margin: 0; accent-color: var(--brand)">'
def note(tone,*c): return f'<div style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px; padding: 10px 16px; border-radius: 4px; border: 1px solid var(--status-{tone}); background: var(--status-{tone}-soft); {T14}">'+''.join(c)+'</div>'

NAV=[('Home','Home.dc.html',''),('Servers','Main.dc.html','34'),('Tools','Tools.dc.html','56'),('Clients','Clients.dc.html','3'),('Profiles','Profiles.dc.html','')]
def navlink(t,h,badge,active,tone=''):
    a='font-weight: 600; color: var(--brand-text); background: var(--brand-soft)' if t==active else 'color: var(--base-content)'
    bd=f'<span style="font-size: 12px; font-weight: 600; padding: 0 7px; border-radius: 9999px; color: var(--{"status-"+tone if tone else "muted"}); background: var(--{"status-"+tone+"-soft" if tone else "base-200"})">{badge}</span>' if badge else ''
    return f'<a href="{h}"{" aria-current=page" if t==active else ""} style="display: flex; justify-content: space-between; align-items: center; padding: 6px 8px; border-radius: 4px; {T14}; {a}; text-decoration: none">{t} {bd}</a>'
def grp(t): return f'<span style="padding: 0 8px 4px; font-size: 12px; line-height: 16px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: var(--muted)">{t}</span>'
def shell(title,active,body,maxw='',overlay=''):
    nav='<nav aria-label="Main" style="flex: 1 1 232px; max-width: 232px; box-sizing: border-box; background: var(--base-100); border-right: 1px solid var(--border); padding: 16px 12px; display: flex; flex-direction: column; gap: 20px">'
    nav+=f'<a href="Home.dc.html" style="display: flex; align-items: center; gap: 8px; padding: 0 8px; color: var(--base-content); text-decoration: none"><img src="{LOGO}" alt="" style="width: 28px; height: 28px"><span style="font-size: 18px; line-height: 28px; font-weight: 700">MCPProxy</span></a>'
    nav+=col(navlink('Home','Home.dc.html','6',active,'error'),*[navlink(t,h,bd,active) for t,h,bd in NAV[1:]],gap=2)
    nav+=col(grp('Protect'),navlink('Review','Review.dc.html','2',active,'review'),navlink('Secrets','Secrets.dc.html','1',active),gap=2)
    nav+=col(grp('Monitor'),navlink('Activity','Activity.dc.html','',active),navlink('Usage','Usage.dc.html','',active),gap=2)
    nav+=col(navlink('Setup','Setup.dc.html','',active),navlink('Settings','Settings.dc.html','',active),navlink('Feedback','#','',active),row(menu('Theme'),'<button type="button" aria-label="Collapse sidebar" style="margin-left: auto; padding: 6px 10px; border: 1px solid var(--border); border-radius: 4px; background: var(--base-100); color: var(--base-content)">«</button>'),gap=2,x='; margin-top: auto')+'</nav>'
    hdr=f'<header style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px; padding: 12px 24px; background: var(--base-100); border-bottom: 1px solid var(--border)"><button type="button" style="flex: 1 1 240px; max-width: 380px; display: flex; justify-content: space-between; align-items: center; padding: 8px 12px; border: 1px solid var(--border); border-radius: 4px; background: var(--base-100); color: var(--muted); {T14}">Search servers, tools, settings <span style="font-family: var(--font-mono); font-size: 12px">⌘K</span></button><span style="margin-left: auto"></span><a href="Main.dc.html" style="{T14}; color: var(--muted)">28 of 34 online</a><button type="button" aria-haspopup="true" style="border: 0; background: none; padding: 0; {T14}; font-weight: 600; color: var(--status-error); text-decoration: underline">6 need attention</button><span style="display: inline-flex">{btn("Add server","p","; border-radius: 4px 0 0 4px")}<button type="button" aria-label="Add client, token or profile" style="padding: 6px 8px; border: 1px solid var(--brand); border-left: 1px solid var(--on-brand); border-radius: 0 4px 4px 0; background: var(--brand); color: var(--on-brand); font-size: 14px">▾</button></span></header>'
    main=f'<main style="flex: 999 1 560px; min-width: 0; display: flex; flex-direction: column; position: relative">{hdr}<div style="padding: 24px; display: flex; flex-direction: column; gap: 16px{("; max-width: "+maxw) if maxw else ""}">{body}</div>{overlay}</main>'
    return page(title,f'<div style="display: flex; flex-wrap: wrap; min-height: 900px; background: var(--base-200)">{nav}{main}</div>',1440,900)
def page(title,inner,w,h):
    return f'''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{title}</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<style>
{ROOT}
</style>
</helmet>
{inner}
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{{"$preview":{{"width":{w},"height":{h}}}}}'>
class Component extends DCLogic {{
  renderVals() {{ return {{}}; }}
}}
</script>
</body>
</html>
'''
P={}
for _f in ("pages.py","pages2.py","pages3.py","pages4.py"): exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),_f)).read())
for name,html in P.items(): open(os.path.join(D,name),'w').write(html)
print(len(P),'boards written')
