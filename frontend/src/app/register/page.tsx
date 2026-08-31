"use client";

import Link from "next/link";
import { FormEvent, Suspense, useState } from "react";
import { apiFetch, setCustomerAccessToken } from "@/lib/api";
import { AuthForm } from "@/components/auth/AuthForm";
import { GoogleButton } from "@/components/auth/GoogleButton";
import { OAuthReceiver } from "@/components/auth/OAuthReceiver";

type AuthResponse = { access_token: string; expires_in?: number };

export default function RegisterPage() {
  const [error, setError] = useState("");
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      const result = await apiFetch<AuthResponse>("/api/v1/auth/register", {
        method: "POST",
        body: JSON.stringify({ name: form.get("name"), email: form.get("email"), password: form.get("password") }),
      });
      setCustomerAccessToken(result.access_token, result.expires_in);
      window.location.href = "/";
    } catch (err) {
      setError(err instanceof Error ? err.message : "Registrasi gagal");
    }
  }
  return (
    <>
      <Suspense fallback={null}>
        <OAuthReceiver onError={setError} />
      </Suspense>
      <AuthForm
        title="Create Account"
        submitLabel="Register"
        onSubmit={submit}
        error={error}
        includeName
        google={
          <Suspense fallback={null}>
            <GoogleButton />
          </Suspense>
        }
        footer={<Link href="/login">Already have an account? Login</Link>}
      />
    </>
  );
}
