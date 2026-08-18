"use client";

import Link from "next/link";
import { FormEvent, useState } from "react";
import { apiFetch, setCustomerAccessToken } from "@/lib/api";
import { AuthForm } from "@/components/auth/AuthForm";

type AuthResponse = { access_token: string };

export default function LoginPage() {
  const [error, setError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const form = new FormData(event.currentTarget);
    try {
      const result = await apiFetch<AuthResponse>("/api/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ email: form.get("email"), password: form.get("password") }),
      });
      setCustomerAccessToken(result.access_token);
      window.location.href = "/";
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login gagal");
    }
  }

  return (
    <AuthForm
      title="Login"
      submitLabel="Login"
      onSubmit={submit}
      error={error}
      footer={<Link href="/register">Create Account</Link>}
    />
  );
}
