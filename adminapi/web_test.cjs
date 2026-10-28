const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync(__dirname + '/web/index.html', 'utf8');
const source = fs.readFileSync(__dirname + '/web/app.js', 'utf8');
const policy = () => ({idCapture:'required',passkey:'required',password:'disabled',passwordPlacement:'hidden',passwordPolicy:{minLength:12,maxLength:128,lowercase:'allowed',uppercase:'allowed',numbers:'allowed',symbols:'allowed'}});
const config = () => ({environment:'dev',revision:3,updatedAt:new Date().toISOString(),features:{idCapture:true},capture:{messages:{en:'Scan',es:'Escanea',fr:'Scanner'}},onboarding:policy()});
function harness(value=config(), versions=[]) {
  const element = () => ({value:'',hidden:true,checked:false,disabled:false,textContent:'',children:[],append(...children){this.children.push(...children)},replaceChildren(){this.children=[]},scrollIntoView(){}});
  const elements = Object.fromEntries([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],element()]));
  elements.environment.value='dev';
  const context = vm.createContext({document:{getElementById:id=>elements[id],createElement:element},TextEncoder,URL,URLSearchParams,setTimeout,clearTimeout,location:{search:''},fetch:async path=>({ok:true,json:async()=>path==='/settings'?{}:path.endsWith('/history')?versions:value})});
  vm.runInContext(source,context);
  return {elements,run:code=>vm.runInContext(code,context),async load(){await vm.runInContext('load()',context)},submit(){elements.config.onsubmit({preventDefault(){}})}};
}
test('loads and submits every enrollment field without mutating current', async()=>{
  const original=config(), h=harness(original); await h.load();
  assert.equal(h.elements.idCaptureRequirement?.value,'required');
  h.elements.idCaptureRequirement.value='disabled'; h.elements.passkeyRequirement.value='optional';
  h.elements.passwordRequirement.value='required'; h.elements.passwordRequirement.onchange();
  h.elements.passwordPlacement.value='with_email'; h.elements.minLength.value='16';h.elements.maxLength.value='64';h.elements.lowercase.value='required';h.elements.symbols.value='disallowed';h.submit();
  const proposed=JSON.parse(h.run('JSON.stringify(proposed)'));
  assert.equal(proposed.config.features.idCapture,false);assert.equal(proposed.config.onboarding.password,'required');assert.equal(proposed.config.onboarding.passwordPlacement,'with_email');
  assert.equal(proposed.config.onboarding.passwordPolicy.minLength,16);assert.equal(proposed.config.onboarding.passwordPolicy.symbols,'disallowed');assert.deepEqual(original,configWithTime(original.updatedAt));
  for(const label of ['ID capture','Passkey','Password requirement','Password placement','Minimum length','Maximum length','Lowercase','Symbols']) assert.ok(h.elements.diff.textContent.includes(label),label);
});
function configWithTime(updatedAt){return {...config(),updatedAt}}
test('password requirement and placement stay consistent in either direction', async()=>{
 const h=harness();await h.load();
 h.elements.passwordRequirement.value='optional';h.elements.passwordRequirement.onchange();assert.equal(h.elements.passwordPlacement.value,'after_verification');
 h.elements.passwordPlacement.value='hidden';h.elements.passwordPlacement.onchange();assert.equal(h.elements.passwordRequirement.value,'disabled');
 h.elements.passwordPlacement.value='with_email';h.elements.passwordPlacement.onchange();assert.equal(h.elements.passwordRequirement.value,'optional');
 h.elements.passwordRequirement.value='disabled';h.elements.passwordRequirement.onchange();assert.equal(h.elements.passwordPlacement.value,'hidden');
});
test('invalid lengths and an empty character domain block review',async()=>{
 const h=harness();await h.load();
 for(const [min,max] of [[7,128],[12,129],[20,12],[8.5,128]]){h.elements.minLength.value=String(min);h.elements.maxLength.value=String(max);h.submit();assert.equal(h.run('proposed'),null);assert.match(h.elements.message.textContent,/length/i)}
 h.elements.minLength.value='12';h.elements.maxLength.value='128';for(const id of ['lowercase','uppercase','numbers','symbols']) h.elements[id].value='disallowed';h.submit();assert.equal(h.run('proposed'),null);assert.match(h.elements.message.textContent,/class/i);
});
test('legacy loading and restore review normalize without changing history',async()=>{
 const legacy=config();delete legacy.onboarding;legacy.revision=1;legacy.features.idCapture=false;
 const h=harness(config(),[{config:legacy,action:'publish',actor:'admin'}]);await h.load();h.elements.history.children[0].children[2].onclick();
 assert.match(h.elements.diff.textContent,/ID capture.*required.*disabled/);assert.match(h.elements.diff.textContent,/Restore revision 1/);assert.equal(legacy.onboarding,undefined);
 const old=harness({...legacy,features:{idCapture:true}});await old.load();assert.equal(old.elements.idCaptureRequirement.value,'optional');assert.equal(old.elements.passkeyRequirement.value,'required');assert.equal(old.elements.passwordRequirement.value,'disabled');assert.equal(old.elements.minLength.value,12);
});

test('restoring a legacy revision reviews every password rule and enrollment change', async () => {
  const active = config();
  active.onboarding.passkey = 'optional';
  active.onboarding.password = 'required';
  active.onboarding.passwordPlacement = 'with_email';
  active.onboarding.passwordPolicy = {
    minLength: 16, maxLength: 64,
    lowercase: 'required', uppercase: 'required', numbers: 'required', symbols: 'disallowed',
  };
  const legacy = config();
  delete legacy.onboarding;
  legacy.revision = 1;
  const h = harness(active, [{ config: legacy, action: 'publish', actor: 'admin' }]);
  await h.load();
  h.elements.history.children[0].children[2].onclick();
  for (const label of ['ID capture', 'Passkey', 'Password requirement', 'Password placement',
    'Minimum length', 'Maximum length', 'Lowercase', 'Uppercase', 'Numbers', 'Symbols']) {
    assert.ok(h.elements.diff.textContent.includes(label), label);
  }
  assert.equal(h.run('proposed.rollbackRevision'), 1);
  assert.equal(h.run('proposed.expectedRevision'), 3);
  assert.equal(active.onboarding.password, 'required');
  assert.equal(legacy.onboarding, undefined);
});
