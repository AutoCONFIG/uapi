import { LoginForm } from "@/components/login-form";

export default function HomePage() {
  return (
    <main className="form-page">
      <section className="auth-card">
        <LoginForm />
      </section>
    </main>
  );
}
