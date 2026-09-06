import { useEffect, useState } from 'react'
import './App.css'

type BackendState = 'checking' | 'online' | 'offline'
const entries = ['仪表盘', '题库', '题目工作台', 'RAG 问答', '复习队列', '设置']

function App() {
  const [backend, setBackend] = useState<BackendState>('checking')
  useEffect(() => {
    fetch('/api/v1/health')
      .then((response) => { if (!response.ok) throw new Error(); setBackend('online') })
      .catch(() => setBackend('offline'))
  }, [])

  return <div className="shell">
    <aside className="sidebar">
      <div className="brand"><span className="brand-mark">M</span><span>Memory Agent</span></div>
      <nav>{entries.map((entry, index) => <button className={index === 0 ? 'nav-item active' : 'nav-item'} key={entry}>{entry}</button>)}</nav>
      <div className="sidebar-footer"><span className={`status-dot ${backend}`} />后端 {backend === 'online' ? '已连接' : backend === 'offline' ? '未连接' : '检查中'}</div>
    </aside>
    <main className="content">
      <header className="topbar"><span className="eyebrow">个人学习空间</span><span className="date">MVP 基线</span></header>
      <section className="welcome"><p className="kicker">仪表盘</p><h1>继续沉淀你的解题记忆。</h1><p className="muted">从题库记录、复盘和可追溯的 AI 对话开始。</p></section>
      <section className="grid">
        <article className="panel primary"><span className="panel-label">题库</span><strong>0</strong><span className="muted">道题目已记录</span><button className="action">打开题库 <span>→</span></button></article>
        <article className="panel"><span className="panel-label">今日复习</span><strong>0</strong><span className="muted">暂无到期任务</span></article>
        <article className="panel"><span className="panel-label">索引状态</span><strong className="state">未配置</strong><span className="muted">配置模型后开始建立记忆索引</span></article>
      </section>
    </main>
  </div>
}

export default App
