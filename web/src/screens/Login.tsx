import { useState } from 'react'
import { Icon, ICONS } from '../components/Parts'
import { ThemeSwitcher } from '../components/Nav'
import { useTitle } from '../lib'
import { Btn, Field } from '../ui'

// Public sign-in page. The form posts natively to /auth/break-glass (form-encoded; the server redirects to /).
export default function Login() {
  useTitle('Sign in')
  const [busy, setBusy] = useState(false)
  return (
    <main className="hero min-h-screen bg-base-200 p-4 relative">
      <div className="absolute top-2 right-2"><ThemeSwitcher /></div>
      <div className="hero-content flex-col lg:flex-row-reverse gap-8 w-full max-w-4xl">
        <div className="text-center lg:text-left">
          <p className="flex items-center justify-center lg:justify-start gap-2 text-xl font-bold my-0"><Icon d={ICONS.shield} className="size-7 text-primary" />Control 8</p>
          <h1 className="text-4xl font-bold mt-2">Deploy with a safety net</h1>
          <p>Sign in with your identity provider. The break-glass password is for emergencies only and every use is audited.</p>
        </div>
        <div className="card w-full max-w-sm bg-base-100 shadow-xl">
          <div className="card-body">
            <h2 className="card-title mt-0">Sign in</h2>
            <a className="btn btn-primary" href="/auth/login">Sign in with OIDC</a>
            <div className="divider">or</div>
            <form method="post" action="/auth/break-glass" onSubmit={() => setBusy(true)}>
              <Field label="Break-glass password" vhint="Enter the password">
                <input className="input validator w-full" type="password" name="password" autoComplete="current-password" required />
              </Field>
              <Btn type="submit" busy={busy} className="btn btn-outline btn-block mt-2">Sign in</Btn>
            </form>
          </div>
        </div>
      </div>
    </main>
  )
}
