'use client';
import {useEffect, useRef, type ReactNode} from 'react';
import {Zap,ArrowUpRight} from 'lucide-react';
import type {SiteInfo} from '../lib/catalog';
import {SessionControl,type TestingSession} from '../app/testing';
export function SiteHeader({site,session,active,children}:{site:SiteInfo;session:TestingSession;active:'home'|'docs'|'playground';children?:ReactNode}){
 const header = useRef<HTMLElement>(null);
 useEffect(() => {
  const node = header.current;
  const container = node?.parentElement;
  if (!node || !container) return;
  const measure = () => container.style.setProperty('--header-height', `${Math.ceil(node.getBoundingClientRect().height)}px`);
  measure();
  const observer = new ResizeObserver(measure);
  observer.observe(node);
  return () => { observer.disconnect(); container.style.removeProperty('--header-height'); };
 }, []);
 return <header ref={header} className="site-header"><div className="site-header-inner"><a className="site-brand" href="/" target="_blank" rel="noopener noreferrer" aria-label={site.name+' 首页'}><span className="site-brand-mark"><Zap size={21} aria-hidden="true"/></span><span><strong>{site.name}</strong><small>{site.subtitle}</small></span></a><nav className="site-nav" aria-label="网站导航"><a href="/" target="_blank" rel="noopener noreferrer" aria-current={active==='home'?'page':undefined}>首页</a><a href="/docs" target="_blank" rel="noopener noreferrer" aria-current={active==='docs'?'page':undefined}>接口文档</a>{session.username&&session.canTest&&<a href="/playground" target="_blank" rel="noopener noreferrer" aria-current={active==='playground'?'page':undefined}>在线测试</a>}<a href="/docs?view=guide" target="_blank" rel="noopener noreferrer">接入指南</a></nav>{children}<div className="site-account"><a href="/account" target="_blank" rel="noopener noreferrer">用户中心<ArrowUpRight size={13} aria-hidden="true"/></a><SessionControl session={session}/></div></div></header>
}
export function SiteFooter({site}:{site:SiteInfo}){return <footer className="site-footer"><div><strong>{site.name}</strong><span>{site.footer}</span></div><nav aria-label="页脚导航"><a href="/docs" target="_blank" rel="noopener noreferrer">接口文档</a><a href="/docs?view=guide" target="_blank" rel="noopener noreferrer">接入指南</a>{site.contact_email&&<a href={'mailto:'+encodeURIComponent(site.contact_email).replace('%40','@')}>{site.contact_email}</a>}</nav></footer>}
