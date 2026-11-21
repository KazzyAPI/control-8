import { useTitle } from '../lib'

// Public sign-in page. The form posts natively to /auth/break-glass (form-encoded; the server redirects to /).
export default function Login() {
  useTitle('Sign in')
  return (
    <main className="hero min-h-screen bg-base-200 p-4">
      <div className="card w-full max-w-sm bg-base-100 shadow-xl">
        <div className="card-body">
          <h1 className="card-title text-2xl">Control 8</h1>
          <p>Sign in to continue.</p>
          <a className="btn btn-primary" href="/auth/login">Sign in with OIDC</a>
          <div className="divider">or</div>
          <form method="post" action="/auth/break-glass" className="flex flex-col gap-2">
            <label className="flex flex-col gap-1">Break-glass password
              <input className="input w-full" type="password" name="password" autoComplete="current-password" required /></label>
            <button className="btn">Sign in</button>
          </form>
        </div>
      </div>
    </main>
  )
}
