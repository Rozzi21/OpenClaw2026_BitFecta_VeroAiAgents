import type { ChatOrderGate } from "./api.ts";

// Structured guest-order codes the backend can attach to a chat turn or a REST
// error envelope. Mirrors services.CodeGuestOrderLimitReached /
// services.CodeOrderCreated / services.CodeOrderAlreadyExists.
//
// The rule they describe (a guest gets exactly one order) is enforced by the
// backend inside the booking transaction. Nothing here re-implements or predicts
// it: the UI only reacts to a decision that already happened.
export const GUEST_ORDER_LIMIT_REACHED = "GUEST_ORDER_LIMIT_REACHED";
export const ORDER_CREATED = "ORDER_CREATED";
export const ORDER_ALREADY_EXISTS = "ORDER_ALREADY_EXISTS";

// OrderGateView is what the chat UI renders: whether to offer sign-in
// (email/password + Google), which order stays trackable, and the headline to
// show. Derived from the structured code ONLY — never from the assistant's
// message text, which is model-generated and unstable.
export type OrderGateView = {
  authRequired: boolean;
  trackOrderId: string | null;
  headline: string;
};

export function orderGateView(gate?: ChatOrderGate | null): OrderGateView | null {
  if (!gate || !gate.code) {
    return null;
  }
  const trackOrderId = gate.order_id ? gate.order_id : null;
  switch (gate.code) {
    case GUEST_ORDER_LIMIT_REACHED:
      // Guest allowance spent. Signing in is what unblocks another order; the
      // existing order stays reachable and is claimed to the account on login.
      return {
        authRequired: true,
        trackOrderId,
        headline: "Your guest order has already been used. Sign in to create another order.",
      };
    case ORDER_CREATED:
      return {
        authRequired: false,
        trackOrderId,
        headline: "Your order has been created. Keep tracking it here, or sign in to create another order.",
      };
    case ORDER_ALREADY_EXISTS:
      return {
        authRequired: false,
        trackOrderId,
        headline: "This chat already has an order. Continue tracking it below.",
      };
    default:
      // Unknown/new backend code: show nothing rather than guessing. The
      // assistant's text still explains the situation to the user.
      return null;
  }
}
