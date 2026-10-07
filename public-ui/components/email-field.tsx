'use client';
import {useEffect,useState} from 'react';
import {normalizeEmailDomains} from '../lib/account-validation';
export function supportedEmail(local:string,domain:string){return local&&domain?local+'@'+domain:''}
export function EmailField({domains:configuredDomains,disabled=false,onValue}:{domains?:string[]|null;disabled?:boolean;onValue?:(email:string)=>void}){
 const domains=normalizeEmailDomains(configuredDomains);
 const [local,setLocal]=useState(''),[selected,setSelected]=useState(domains[0]||''),[plain,setPlain]=useState('');const domain=domains.includes(selected)?selected:domains[0]||'';const value=domains.length?supportedEmail(local,domain):plain;
 useEffect(()=>{onValue?.(value)},[value,onValue]);
 return <label className="email-field">邮箱{domains.length?<><div className="email-address-input"><input type="text" name="email_local" aria-label="邮箱前缀" placeholder="邮箱前缀" autoComplete="off" maxLength={64} pattern="[^\s@]+" required disabled={disabled} value={local} onChange={e=>setLocal(e.target.value)}/><select name="email_domain" aria-label="邮箱后缀" disabled={disabled} value={domain} onChange={e=>setSelected(e.target.value)}>{domains.map(v=><option value={v} key={v}>@{v}</option>)}</select></div><input type="hidden" name="email" value={value}/></>:<input name="email" type="email" autoComplete="email" maxLength={254} required disabled={disabled} value={plain} onChange={e=>setPlain(e.target.value)}/>}</label>
}
