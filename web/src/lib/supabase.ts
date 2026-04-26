import { createClient } from '@supabase/supabase-js'

const validateEnvValue = (name: string, value: unknown, minLength: number): string => {
  if (typeof value !== 'string') {
    throw new Error(`${name} must be a string`)
  }

  const trimmed = value.trim()
  if (trimmed.length < minLength) {
    throw new Error(`${name} must be at least ${minLength} characters`)
  }

  return trimmed
}

const supabaseUrlRaw = validateEnvValue('VITE_SUPABASE_URL', import.meta.env.VITE_SUPABASE_URL, 8)
const supabaseAnonKey = validateEnvValue(
  'VITE_SUPABASE_ANON_KEY',
  import.meta.env.VITE_SUPABASE_ANON_KEY,
  20,
)

let supabaseUrl: string
try {
  supabaseUrl = new URL(supabaseUrlRaw).toString()
} catch {
  throw new Error('VITE_SUPABASE_URL must be a valid URL')
}

export const supabase = createClient(supabaseUrl, supabaseAnonKey)
