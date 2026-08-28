"use client";

import { FormEvent } from "react";

type AuthFormProps = {
  title: string;
  submitLabel: string;
  onSubmit: (e: FormEvent<HTMLFormElement>) => void;
  error: string;
  footer: React.ReactNode;
  includeName?: boolean;
  // google renders the "Continue with Google" OAuth button ABOVE the
  // credential inputs, with an "or" divider beneath it (optional so existing
  // callers are unaffected).
  google?: React.ReactNode;
};

export function AuthForm({ title, submitLabel, onSubmit, error, footer, includeName = false, google }: AuthFormProps) {
  return (
    <main className="min-h-screen bg-slate-50 px-6 py-16">
      <form onSubmit={onSubmit} className="mx-auto max-w-md space-y-4 rounded-3xl bg-white p-8 shadow-sm">
        <h1 className="text-3xl font-black">{title}</h1>
        {google ? (
          <>
            {google}
            <div className="flex items-center gap-3 text-xs font-semibold uppercase tracking-wide text-slate-400">
              <span className="h-px flex-1 bg-slate-200" />
              or
              <span className="h-px flex-1 bg-slate-200" />
            </div>
          </>
        ) : null}
        {includeName ? <input name="name" required minLength={2} placeholder="Name" className="w-full rounded-xl border p-3" /> : null}
        <input name="email" type="email" required placeholder="Email" className="w-full rounded-xl border p-3" />
        <input name="password" type="password" required minLength={8} placeholder="Password" className="w-full rounded-xl border p-3" />
        {error ? <p className="text-sm text-rose-700">{error}</p> : null}
        <button className="w-full rounded-xl bg-[#df3333] p-3 font-bold text-white">{submitLabel}</button>
        <div className="text-center text-sm text-[#df3333]">{footer}</div>
      </form>
    </main>
  );
}
