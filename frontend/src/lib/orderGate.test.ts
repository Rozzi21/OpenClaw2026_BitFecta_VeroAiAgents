// The chat UI must decide what to show from the backend's STRUCTURED code, never
// from the assistant's wording. These tests lock that: only known codes produce
// a gate, only the guest-limit code asks for sign-in, and an order id is shown
// exactly when the backend sent one.
// Run: npm test
import { test } from "node:test";
import assert from "node:assert/strict";

import { orderGateView } from "./orderGate.ts";

test("guest limit asks for sign-in and never invents an order id", () => {
  const view = orderGateView({ code: "GUEST_ORDER_LIMIT_REACHED", auth_required: true });
  assert.ok(view);
  assert.equal(view.authRequired, true);
  assert.equal(view.trackOrderId, null);
  assert.match(view.headline, /sign in/i);
});

test("created order keeps tracking available without demanding sign-in", () => {
  const view = orderGateView({
    code: "ORDER_CREATED",
    auth_required: false,
    order_id: "11111111-1111-1111-1111-111111111111",
  });
  assert.ok(view);
  assert.equal(view.authRequired, false);
  assert.equal(view.trackOrderId, "11111111-1111-1111-1111-111111111111");
});

test("an order already attached to this chat stays trackable", () => {
  const view = orderGateView({
    code: "ORDER_ALREADY_EXISTS",
    auth_required: false,
    order_id: "22222222-2222-2222-2222-222222222222",
  });
  assert.ok(view);
  assert.equal(view.authRequired, false);
  assert.equal(view.trackOrderId, "22222222-2222-2222-2222-222222222222");
});

test("no gate, an empty code, or an unknown code renders nothing", () => {
  assert.equal(orderGateView(undefined), null);
  assert.equal(orderGateView(null), null);
  assert.equal(orderGateView({ code: "", auth_required: true }), null);
  assert.equal(orderGateView({ code: "SOME_FUTURE_CODE", auth_required: true }), null);
});

test("auth_required is taken from the code, not from the flag alone", () => {
  // A tampered/garbled payload claiming auth_required on a created order must
  // not turn the success state into a sign-in wall: the code decides.
  const view = orderGateView({ code: "ORDER_CREATED", auth_required: true, order_id: "abc" });
  assert.ok(view);
  assert.equal(view.authRequired, false);
});

test("assistant prose is never an input: identical text, different codes", () => {
  // Two turns whose assistant text would read the same way; only the code
  // differs, and only the code changes the UI.
  const blocked = orderGateView({ code: "GUEST_ORDER_LIMIT_REACHED", auth_required: true });
  const created = orderGateView({ code: "ORDER_CREATED", auth_required: false, order_id: "x" });
  assert.notEqual(blocked?.authRequired, created?.authRequired);
});
