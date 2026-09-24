const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const script = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.js'), 'utf8');
const from = script.indexOf('function shellQuote(');
const to = script.indexOf('function pluginRow(', from);
assert.ok(from >= 0 && to > from);
const context = {location:{origin:'http://localhost:8080'}, can() {return true;}, esc(value) {return String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));}};
vm.createContext(context);
vm.runInContext(script.slice(from, to), context);

const game = {name:'game-discount-wasm', enabled:true, manifest: {routes: [
  {name:'服务状态', method:'GET', path:'/api/game-discount-wasm/v1/status', auth_mode:'api_key'},
  {name:'报价列表', method:'GET', path:'/api/game-discount-wasm/v1/offers', auth_mode:'api_key', example_query:{region:'HK'}},
  {name:'单品查询', method:'GET', path:'/api/game-discount-wasm/v1/offers/{external_id}', auth_mode:'api_key', example_path_params:{external_id:'wasm-demo-001'}},
]}};
const published = {enabled:true, published_at:'2026-09-23T00:00:00Z'};
test('manifest routes are listed before binding and derive examples from declarations', () => {
  const html = context.pluginUsage(game, []);
  assert.equal((html.match(/class="plugin-route is-missing"/g) || []).length, 3);
  assert.match(html, /未配置/);
  assert.match(html, /X-API-Key: 你的 API Key/);
  assert.match(html, /offers\?region=HK/);
  assert.match(html, /offers\/wasm-demo-001/);
  assert.match(html, /data-plugin-route="\/api\/game-discount-wasm\/v1\/status"/);
});
test('only declared routes get one-click creation; unknown and legacy plugins do not get guessed status', () => {
  const html = context.pluginUsage({name:'Game_Discount',enabled:true,manifest:{}}, []);
  assert.match(html, /未声明接口/);
  assert.doesNotMatch(html, /data-plugin-route=/);
  assert.doesNotMatch(html, /game-discount\/v1\/status/);
  const legacy = context.pluginUsage({name:'Game_Discount',enabled:true}, [{plugin:'Game_Discount',name:'actual',method:'POST',path:'/api/real',auth_mode:'none',...published}]);
  assert.match(legacy, /\/api\/real/);
  assert.match(legacy, /1 条已发布接口/);
  assert.doesNotMatch(legacy, /data-plugin-route=/);
});
test('Game_Discount uses its own manifest, never the old demo examples', () => {
  const item = {name:'Game_Discount',enabled:true,manifest:{routes:[{name:'查询',method:'GET',path:'/api/game-discount/v2/lookup/{external_id}',auth_mode:'none',example_path_params:{external_id:'custom-123'},example_query:{currency:'USD'}}]}};
  const html = context.pluginUsage(item, []);
  assert.match(html, /game-discount\/v2\/lookup\/custom-123\?currency=USD/);
  assert.doesNotMatch(html, /wasm-demo-001|region=HK|game-discount\/v1\/status/);
  assert.equal((html.match(/class="plugin-route is-missing"/g) || []).length, 1);
});
test('a partial binding keeps remaining manifest suggestions and published auth takes precedence', () => {
  const html = context.pluginUsage(game, [{plugin:game.name,name:'status',method:'GET',path:'/api/game-discount-wasm/v1/status',auth_mode:'jwt',...published}]);
  assert.equal((html.match(/class="plugin-route /g) || []).length, 3);
  assert.equal((html.match(/class="plugin-route is-missing"/g) || []).length, 2);
  assert.match(html, /1 条已发布接口/);
  assert.match(html, /Authorization: Bearer 你的 JWT/);
  assert.match(html, /data-plugin-route="\/api\/game-discount-wasm\/v1\/offers"/);
});
test('manifest method and auth are passed to the create interface button', () => {
  const item = {name:'custom',enabled:true,manifest:{routes:[{name:'创建',method:'POST',path:'/api/custom/v1/items',auth_mode:'none'}]}};
  const html = context.pluginUsage(item, []);
  assert.match(html, /data-plugin-method="POST"/);
  assert.match(html, /data-plugin-auth="none"/);
  assert.match(html, /data-plugin-title="创建"/);
});
test('conflicting route disables creation and unrelated configured route remains visible', () => {
  const html = context.pluginUsage(game, [
    {plugin:'other',method:'GET',path:'/api/game-discount-wasm/v1/status',...published},
    {plugin:game.name,name:'custom',method:'POST',path:'/api/custom',auth_mode:'none',...published},
  ]);
  assert.match(html, /路径已占用/);
  assert.doesNotMatch(html, /data-plugin-route="\/api\/game-discount-wasm\/v1\/status"/);
  assert.match(html, /POST/);
  assert.match(html, /api\/custom/);
});
test('examples escape query/path values, shell quotes and HTML', () => {
  const command = context.pluginCallCommand({name:'custom'}, {method:'GET',path:'/api/custom/{id}',auth_mode:'none',example_path_params:{id:'two words'},example_query:{'sort by':"a'b"}});
  assert.match(command, /two%20words/);
  assert.match(command, /sort%20by=a.*b/);
  assert.match(context.pluginCallCommand({name:'unsafe'}, {method:'GET',path:"/api/o'hare"}), /'"'"'/);
  const html = context.pluginUsage({name:'unsafe',enabled:true}, [{plugin:'unsafe',name:'bad',method:'GET',path:"/api/o'hare",auth_mode:'none',enabled:false}]);
  assert.match(html, /&#39;/);
});
test('plugin version always exposes an uninstall action', () => {
  const from = script.indexOf('function pluginRow(');
  const to = script.indexOf('async function uploadPlugin(', from);
  const rowContext = {can() {return true;}, esc(value) {return String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));}};
  vm.createContext(rowContext);
  vm.runInContext(script.slice(from, to), rowContext);
  const enabled = rowContext.pluginRow({id:'plugin_1', name:'inventory-demo', version:'1.0.0', runtime:'wasm', checksum:'abcdef1234567890', enabled:true});
  const disabled = rowContext.pluginRow({id:'plugin_2', name:'inventory-demo', version:'0.9.0', runtime:'wasm', checksum:'abcdef1234567890', enabled:false});
  assert.match(enabled, /卸载/);
  assert.match(disabled, /卸载/);
  assert.match(enabled, /data-enabled="true"/);
  assert.match(disabled, /data-enabled="false"/);
});