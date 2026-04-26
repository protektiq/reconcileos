import { useEffect } from 'react'
import { Navigate, Outlet, RouterProvider, createBrowserRouter } from 'react-router-dom'
import { AppLayout } from './components/layout/AppLayout'
import { supabase } from './lib/supabase'
import { AttestationsPage } from './pages/AttestationsPage'
import { AuthCallbackPage } from './pages/AuthCallbackPage'
import { DashboardPage } from './pages/DashboardPage'
import { LoginPage } from './pages/LoginPage'
import { MarketplacePage } from './pages/MarketplacePage'
import { NotFoundPage } from './pages/NotFoundPage'
import { QueuePage } from './pages/QueuePage'
import { ProtectedRoute } from './routes/ProtectedRoute'
import { useAuthStore } from './stores/authStore'

const RootRedirect = () => {
  const session = useAuthStore((state) => state.session)
  return <Navigate to={session ? '/dashboard' : '/login'} replace />
}

const ProtectedShell = () => (
  <ProtectedRoute>
    <AppLayout>
      <Outlet />
    </AppLayout>
  </ProtectedRoute>
)

const router = createBrowserRouter([
  { path: '/', element: <RootRedirect /> },
  { path: '/login', element: <LoginPage /> },
  { path: '/auth/callback', element: <AuthCallbackPage /> },
  {
    element: <ProtectedShell />,
    children: [
      { path: '/dashboard', element: <DashboardPage /> },
      { path: '/marketplace', element: <MarketplacePage /> },
      { path: '/queue', element: <QueuePage /> },
      { path: '/attestations', element: <AttestationsPage /> },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
])

const AuthBootstrap = () => {
  const setSession = useAuthStore((state) => state.setSession)
  const clearSession = useAuthStore((state) => state.clearSession)

  useEffect(() => {
    const initializeSession = async () => {
      const { data } = await supabase.auth.getSession()
      if (data.session) {
        setSession(data.session)
        return
      }

      clearSession()
    }

    void initializeSession()

    const { data: authSubscription } = supabase.auth.onAuthStateChange((_event, session) => {
      if (session) {
        setSession(session)
        return
      }

      clearSession()
    })

    return () => authSubscription.subscription.unsubscribe()
  }, [clearSession, setSession])

  return <RouterProvider router={router} />
}

const App = () => <AuthBootstrap />

export default App
