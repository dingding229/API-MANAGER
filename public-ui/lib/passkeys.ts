export type PasskeyTools={resetPreference:()=>void;preference:(fresh?:boolean)=>Promise<{method:string;passkey_available:boolean}>;supported:()=>boolean;ceremony:(purpose:'login'|'register'|'confirm',body?:Record<string,unknown>,start?:unknown)=>Promise<any>;prepare:(path:string,init:RequestInit,force?:boolean)=>Promise<Request>};
declare global {interface Window {APIManagerPasskeys?:PasskeyTools}}
let pending:Promise<PasskeyTools>|undefined;
export async function passkeyTools():Promise<PasskeyTools>{
 if(window.APIManagerPasskeys)return window.APIManagerPasskeys;
 if(!pending)pending=new Promise((resolve,reject)=>{const script=document.createElement('script');script.src='/ui/passkeys.js';script.onload=()=>window.APIManagerPasskeys?resolve(window.APIManagerPasskeys):reject(Error('通行密钥功能暂不可用'));script.onerror=()=>{pending=undefined;script.remove();reject(Error('通行密钥功能暂不可用'))};document.head.append(script)});
 return pending;
}
