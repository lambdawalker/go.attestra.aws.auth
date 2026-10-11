'use strict';
const es=document.documentElement.lang==='es';
for(const pre of document.querySelectorAll('pre')){
 const button=document.createElement('button');button.className='copy';button.textContent=es?'Copiar':'Copy';button.type='button';
 button.addEventListener('click',async()=>{try{await navigator.clipboard.writeText(pre.querySelector('code')?.textContent||pre.textContent);button.textContent=es?'Copiado':'Copied';}catch{button.textContent=es?'Selecciona el código':'Select code';}});
 pre.before(button);
}
const input=document.querySelector('#search'),results=document.querySelector('#results');let catalog;
input?.addEventListener('input',async()=>{
 if(!catalog){try{catalog=await fetch(input.dataset.index).then(r=>r.json());}catch{return;}}
 const term=input.value.trim().toLowerCase();results.replaceChildren();if(term.length<2)return;
 for(const item of catalog.filter(p=>(p.title+' '+p.text).toLowerCase().includes(term)).slice(0,10)){
  const li=document.createElement('li'),a=document.createElement('a');a.href=item.url;a.textContent=item.title;li.append(a);results.append(li);
 }
});
