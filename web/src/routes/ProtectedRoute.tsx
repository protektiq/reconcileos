import type { ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuthStore } from '../stores/authStore'

type ProtectedRouteProps = {
  children: ReactNode
}

export const ProtectedRoute = ({ children }: ProtectedRouteProps) => {
  const session = useAuthStore((state) => state.session)

  if (!session) {
    return <Navigate to="/login" replace />
  }

  return <>{children}</>
}
