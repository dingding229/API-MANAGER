'use client';

export default function Error({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return <main className="route-state" role="alert"><div className="state-card"><h1>公开目录暂时不可用</h1><p>请稍后重试。如果问题持续，请联系服务维护人员。</p><button className="retry-button" onClick={() => reset()}>重新加载</button></div></main>;
}
