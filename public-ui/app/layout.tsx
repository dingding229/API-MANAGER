import type { Metadata } from 'next';
import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';

export const metadata: Metadata = {
  title: 'API Manager · 开放接口目录',
  description: '浏览已公开的 API 接口、参数和调用方式。',
  referrer: 'no-referrer',
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="zh-CN" suppressHydrationWarning><body><RootProvider theme={{ enabled: false }} search={{ enabled: false }}>{children}</RootProvider></body></html>;
}
