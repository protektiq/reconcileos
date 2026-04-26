import type { ReactNode } from 'react'
import { useUiStore } from '../../stores/uiStore'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'

type AppLayoutProps = {
  children: ReactNode
}

export const AppLayout = ({ children }: AppLayoutProps) => {
  const sidebarOpen = useUiStore((state) => state.sidebarOpen)

  return (
    <div className="flex min-h-screen bg-[#060810] text-[#e6f6ff]">
      {sidebarOpen ? <Sidebar /> : null}
      <div className="flex min-h-screen flex-1 flex-col">
        <TopBar />
        <main className="flex-1 bg-[#060810]">{children}</main>
      </div>
    </div>
  )
}
