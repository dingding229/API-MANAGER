package web

import (
	"strings"
	"testing"
)

func TestSessionUIUsesCookiesAndNeverPersistsNewTokens(t *testing.T) {
	data, _ := assets.ReadFile("assets/app.js")
	source := string(data)
	for _, bad := range []string{"state.token", "sessionStorage.setItem('api_manager_session'", "headers.Authorization ="} {
		if strings.Contains(source, bad) {
			t.Fatal("legacy token login path remains", bad)
		}
	}
	for _, required := range []string{"credentials:'same-origin'", "X-API-Request", "BroadcastChannel", "/auth/v1/me", "/auth/v1/session", "bindCacheFields(card.querySelector('#api-form'), id)", "插件数据缓存", "cache_ttl", "cache_limit", "data-cache-manage"} {
		if !strings.Contains(source, required) {
			t.Fatal("missing", required)
		}
	}
}

func TestSuccessfulSharedSessionDiscardsOtherLegacyIdentity(t *testing.T) {
	hydrate := consoleFunction(t, "async function hydrateSession() {", "authEvents?.addEventListener")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
let hydratingSession=false,pendingSessionHydration=false,initialSessionHydration=false,authEpoch=0,legacySession='old-user-token';
const initialLoginControls=[],state={user:null,permissions:[],cache:{}};let migrations=0,loggedOut=false;
const $=()=>({classList:{contains:()=>true}});const $$=()=>[];const showConsole=()=>{};const notice=()=>{};
const closeProfileModal=()=>{},closeRoleModal=()=>{},closeLogCleanupModal=()=>{};
const clearSession=()=>{state.user=null;state.permissions=[];authEpoch++;legacySession=''};const broadcastAuth=()=>{};
const api=async path=>{if(path==='/auth/v1/session'){migrations++;return {}};if(loggedOut){const e=new Error('expired');e.status=401;throw e};return {user:{id:'current-user',username:'current'},permissions:[]}};
`+hydrate+`
(async()=>{await hydrateSession();assert.equal(legacySession,'');loggedOut=true;clearSession();await hydrateSession();assert.equal(migrations,0);assert.equal(state.user,null)})().catch(e=>{console.error(e);process.exit(1)});`)
}
