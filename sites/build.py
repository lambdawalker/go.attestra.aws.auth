#!/usr/bin/env python3
"""Render repository Markdown; never execute historical source or UI prototypes."""
import argparse, hashlib, html, json, os, re, shutil, subprocess
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit, unquote
from html.parser import HTMLParser
import markdown
from markdown.extensions import Extension
from markdown.treeprocessors import Treeprocessor
from pygments.formatters import HtmlFormatter
ROOT=Path(__file__).resolve().parents[1]
CONFIG=json.loads((ROOT/'sites/config.json').read_text())
OUT=ROOT/'sites/dist'
BASE='/'+CONFIG['repository'].split('/')[-1]+'/'
RAW='https://raw.githubusercontent.com/'+CONFIG['repository']+'/'
GIT='https://github.com/'+CONFIG['repository']+'/blob/'

def sources():
 result={}
 for pattern in CONFIG['sources']:
  for p in ROOT.glob(pattern):
   if p.is_file() and p.suffix=='.md' and '/es/' not in str(p):result[p.relative_to(ROOT).as_posix()]=p.read_text()
 return result

def route(path,locale='en',raw=False):
 return BASE+('raw/' if raw else '')+locale+'/main/'+(path if raw else path[:-3]+'.html')

def resolve(current,href,locale,docs,raw=False):
 u=urlsplit(href)
 if u.scheme or u.netloc:
  if raw and u.netloc=='github.com' and '/blob/' in u.path and u.path.endswith('.md'):
   return 'https://raw.githubusercontent.com'+u.path.replace('/blob/','/')+('#'+u.fragment if u.fragment else '')
  return href
 if not u.path:return href
 target=os.path.normpath(os.path.join(os.path.dirname(current),unquote(u.path))).replace(os.sep,'/')
 if target.startswith('../'):raise ValueError(f'{current}: escaping path {href}')
 if target.startswith('docs/es/agents/'):
  target=target.replace('docs/es/agents/','docs/agents/',1)
 if target in docs:
  return route(target,locale,raw)+('#'+u.fragment if u.fragment else '')
 p=ROOT/target
 if not p.exists():raise ValueError(f'{current}: missing target {href}')
 if p.suffix.lower() in ['.png','.svg','.jpg']:
  if raw:return BASE+'assets/'+target
  return BASE+'assets/'+target
 # Intentionally leave the documentation site for executable source / prototypes.
 kind='tree' if p.is_dir() else 'blob'
 ref='main' if target.startswith(('docs/','sites/')) or p.suffix=='.md' else CONFIG['source_ref']
 return f'https://github.com/{CONFIG["repository"]}/{kind}/{ref}/{target}'+('#'+u.fragment if u.fragment else '')

class Links(Treeprocessor):
 def __init__(self,md,current,locale,docs):super().__init__(md);self.current=current;self.locale=locale;self.docs=docs
 def run(self,root):
  for e in root.iter():
   for attr in ['href','src']:
    if e.get(attr):e.set(attr,resolve(self.current,e.get(attr),self.locale,self.docs))
class LinkExtension(Extension):
 def __init__(self,current,locale,docs):self.args=(current,locale,docs);super().__init__()
 def extendMarkdown(self,md):md.treeprocessors.register(Links(md,*self.args),'repo-links',15)

# Tested equivalent for these repositories' inline links: fences and inline code
# are kept intact; unsupported reference-style links fail rather than mispublish.
def raw_markdown(text,current,locale,docs):
 parts=re.split(r'(```[^\n]*\n.*?^```[^\n]*$|~~~[^\n]*\n.*?^~~~[^\n]*$|`[^`\n]+`)',text,flags=re.M|re.S)
 for i in range(0,len(parts),2):
  if re.search(r'^\s*\[[^\]]+\]:',parts[i],re.M):raise ValueError('reference links need explicit support')
  parts[i]=re.sub(r'(!?\[[^\]\n]*\]\()([^\s)]+)(\))',lambda m:m[1]+resolve(current,m[2],locale,docs,True)+m[3],parts[i])
 return ''.join(parts)

def sha(text):return hashlib.sha256(text.encode()).hexdigest()
def translated(current,text,locale):
 if locale=='en':return text,False
 manifest=json.loads((ROOT/'sites/translations.json').read_text())
 record=manifest.get(current)
 if record and record['source_sha256']==sha(text):
  translated=(ROOT/record['path']).read_text()
  fences=lambda s:re.findall(r'^```[^\n]*\n.*?^```',s,re.M|re.S)
  if fences(text)!=fences(translated):raise ValueError('translated code differs: '+current)
  return translated,False
 return text,True

class Inspector(HTMLParser):
 def __init__(self):super().__init__();self.links=[];self.ids=set()
 def handle_starttag(self,tag,attrs):
  a=dict(attrs)
  if 'id'in a:self.ids.add(a['id'])
  for k in ['href','src']:
   if k in a:self.links.append(a[k])

def validate():
 files={}
 for p in OUT.rglob('*.html'):
  parser=Inspector();parser.feed(p.read_text());files[p]=parser
 errors=[]
 for p,parser in files.items():
  for href in parser.links:
   u=urlsplit(href)
   if u.scheme or u.netloc:continue
   if u.path.startswith(BASE):target=OUT/unquote(u.path[len(BASE):])
   elif not u.path:target=p
   else:errors.append(f'{p}: unexpected base path {href}');continue
   if target.is_dir():target=target/'index.html'
   if not target.exists():errors.append(f'{p}: missing {href}')
   elif u.fragment and target in files and unquote(u.fragment) not in files[target].ids:errors.append(f'{p}: missing anchor {href}')
 if errors:raise ValueError('\n'.join(errors[:60]))
 print(f'Validated {len(files)} HTML pages, project-base links and anchors')

def build():
 docs=sources()
 if not docs:raise ValueError('No documentation')
 catalog=json.loads((ROOT/'sites/versions.json').read_text())
 if catalog['releases']:raise ValueError('No release snapshots supported yet: implement immutable rendering before advertising a release')
 if OUT.exists():shutil.rmtree(OUT)
 OUT.mkdir(parents=True)
 (OUT/'.nojekyll').write_text('')
 shutil.copy(ROOT/'sites/style.css',OUT/'style.css');shutil.copy(ROOT/'sites/app.js',OUT/'app.js')
 (OUT/'highlight.css').write_text(HtmlFormatter().get_style_defs('.codehilite'))
 # Only public documentation images; no credentials, generated state or raw HTML.
 for path,text in docs.items():
  for image in re.findall(r'!?\[[^\]]*\]\(([^)]+)\)',text):
   if urlsplit(image).scheme:continue
   src=(ROOT/path).parent/image
   if src.is_file() and src.suffix.lower() in ['.png','.svg','.jpg']:
    rel=src.resolve().relative_to(ROOT);dest=OUT/'assets'/rel;dest.parent.mkdir(parents=True,exist_ok=True);shutil.copy(src,dest)
 manifest=ROOT/'docs/screenshots/manifest.json'
 if manifest.exists():
  for item in json.loads(manifest.read_text())['captures']:
   if hashlib.sha256((ROOT/item['path']).read_bytes()).hexdigest()!=item['image_sha256']:raise ValueError('Unreviewed screenshot change: '+item['path'])
 for locale in ['en','es']:
  search=[]
  for path,english in sorted(docs.items()):
   text,fallback=translated(path,english,locale)
   link_source=json.loads((ROOT/'sites/translations.json').read_text())[path]['path'] if locale=='es' and not fallback else path
   engine=markdown.Markdown(extensions=['fenced_code','tables','toc','codehilite',LinkExtension(link_source,locale,docs)],extension_configs={'codehilite':{'guess_lang':False}})
   content=engine.convert(text)
   title=re.search(r'^# (.+)',text,re.M).group(1) if re.search(r'^# (.+)',text,re.M) else path
   nav=''.join(f'<a href="{route(p,locale)}">{html.escape(labels[locale])}</a>' for p,labels in CONFIG['nav'].items())
   other='es' if locale=='en' else 'en'
   notice=('Traducción no disponible o desactualizada. Se muestra inglés de esta misma revisión.' if fallback else '')
   labels={'en':('Search this documentation','Current source · main · not a package release','Contents','Raw Markdown','Source','Skip to content'),'es':('Buscar en esta documentación','Código actual · main · no es una versión publicada','Contenido','Markdown sin procesar','Fuente','Saltar al contenido')}[locale]
   body=f'''<!doctype html><html lang="{locale}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{html.escape(title)} · {CONFIG['title']}</title><link rel="stylesheet" href="{BASE}style.css"><link rel="stylesheet" href="{BASE}highlight.css"><link rel="canonical" href="https://lambdawalker.github.io{route(path,locale)}"><link rel="alternate" hreflang="{other}" href="{route(path,other)}"><script defer src="{BASE}app.js"></script></head><body><a class="skip" href="#content">{labels[5]}</a><header><a class="brand" href="{route('docs/agents/index.md',locale)}">ATTESTRA <small>{CONFIG['title']}</small></a><nav aria-label="Language"><a href="{route(path,'en')}">English</a><a href="{route(path,'es')}">Español</a><span>main</span></nav></header><div class="layout"><aside><label for="search">{labels[0]}</label><input id="search" type="search" data-index="{BASE}{locale}/search.json"><ul id="results" aria-live="polite"></ul><nav aria-label="{labels[2]}">{nav}</nav><p><a href="{BASE}llms.txt">AI / llms.txt</a></p></aside><main id="content"><p class="scope">{labels[1]} · {CONFIG['source_ref'][:7]}</p>{'<p class="notice" role="status">'+notice+'</p>' if notice else ''}<article lang="{'en' if fallback else locale}">{content}</article><footer><a href="{route(path,locale,True)}">{labels[3]}</a> · <a href="{GIT}main/{path}">{labels[4]}</a><p>Code reference: {CONFIG['source_ref']}. Documentation build: {subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()[:12]}.</p></footer></main></div></body></html>'''
   dest=OUT/(route(path,locale)[len(BASE):]);dest.parent.mkdir(parents=True,exist_ok=True);dest.write_text(body)
   raw=OUT/(route(path,locale,True)[len(BASE):]);raw.parent.mkdir(parents=True,exist_ok=True)
   raw.write_text(('> '+notice+'\n\n' if notice else '')+raw_markdown(text,link_source,locale,docs))
   search.append({'title':title+(' [English fallback]' if fallback else ''),'url':route(path,locale),'text':re.sub('<[^>]+>',' ',content)[:15000]})
  (OUT/locale/'search.json').write_text(json.dumps(search,ensure_ascii=False))
 # Stable discovery entry points contain absolute raw Markdown links.
 (OUT/'agents').mkdir()
 (OUT/'agents/index.md').write_text(raw_markdown(docs['docs/agents/index.md'],'docs/agents/index.md','en',docs))
 if 'IMPORT.md'in docs:(OUT/'IMPORT.md').write_text(raw_markdown(docs['IMPORT.md'],'IMPORT.md','en',docs))
 (OUT/'llms.txt').write_text(f'# {CONFIG["title"]}\n\nCurrent source, not a published release.\n\n- [Agent entry]({BASE}agents/index.md)\n- [Documentation catalog]({BASE}versions.json)\n- [English]({route("docs/agents/index.md","en",True)})\n- [Español; English fallback where marked]({route("docs/agents/index.md","es",True)})\n')
 (OUT/'versions.json').write_text(json.dumps(catalog,indent=2))
 (OUT/'index.html').write_text(f'<!doctype html><html lang="en"><meta charset="utf-8"><title>{CONFIG["title"]}</title><meta http-equiv="refresh" content="0;url={route("docs/agents/index.md")}"><a href="{route("docs/agents/index.md")}">Documentation / Documentación</a></html>')
 validate()
 print(f'Built {len(docs)} canonical pages in English/Spanish (fallbacks explicit). Output: {OUT}')

if __name__=='__main__':build()
