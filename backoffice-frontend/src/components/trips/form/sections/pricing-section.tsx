"use client";

import { FormSection } from "../ui/form-section";
import { Field } from "../ui/field";
import { Checkbox } from "../ui/checkbox";
import { TripFormStaticDefaults } from "../map-trip-to-form";
import { useState, useEffect, useCallback } from "react";

type Props = Pick<
  TripFormStaticDefaults,
  | "base_price"
  | "child_price"
  | "discount_price"
  | "child_discount_price"
  | "discount_percent"
  | "child_discount_percent"
  | "discount_enabled"
  | "child_discount_enabled"
>;

function computePriceFromPercent(
  basePrice: string,
  percent: string
): string {
  const base = parseFloat(basePrice);
  const pct = parseFloat(percent);
  if (isNaN(base) || isNaN(pct) || pct <= 0 || pct >= 100) return "";
  return (base * (1 - pct / 100)).toFixed(2);
}

function computePercentFromPrice(
  basePrice: string,
  discountPrice: string
): string {
  const base = parseFloat(basePrice);
  const discount = parseFloat(discountPrice);
  if (
    isNaN(base) ||
    isNaN(discount) ||
    base <= 0 ||
    discount <= 0 ||
    discount >= base
  )
    return "";
  return String(Math.round(((base - discount) / base) * 100));
}

export function PricingSection({
  base_price: initialBase = "",
  child_price: initialChild = "",
  discount_price: initialDiscount = "",
  child_discount_price: initialChildDiscount = "",
  discount_percent: initialDiscountPct = "",
  child_discount_percent: initialChildDiscountPct = "",
  discount_enabled = false,
  child_discount_enabled = false,
}: Partial<Props> = {}) {
  const [basePrice, setBasePrice] = useState(initialBase);
  const [childPrice, setChildPrice] = useState(initialChild);
  const [discountPrice, setDiscountPrice] = useState(initialDiscount);
  const [childDiscountPrice, setChildDiscountPrice] = useState(
    initialChildDiscount
  );
  const [discountPct, setDiscountPct] = useState(initialDiscountPct);
  const [childDiscountPct, setChildDiscountPct] = useState(
    initialChildDiscountPct
  );
  const [discountEnabled, setDiscountEnabled] = useState(discount_enabled);
  const [childDiscountEnabled, setChildDiscountEnabled] = useState(
    child_discount_enabled
  );
  const [discountDirty, setDiscountDirty] = useState<
    "none" | "percent" | "price"
  >("none");
  const [childDiscountDirty, setChildDiscountDirty] = useState<
    "none" | "percent" | "price"
  >("none");

  // Reset local state when initial values change (e.g. edit mode prefill)
  useEffect(() => {
    setBasePrice(initialBase);
    setChildPrice(initialChild);
    setDiscountPrice(initialDiscount);
    setChildDiscountPrice(initialChildDiscount);
    setDiscountPct(initialDiscountPct);
    setChildDiscountPct(initialChildDiscountPct);
    setDiscountEnabled(discount_enabled);
    setChildDiscountEnabled(child_discount_enabled);
    setDiscountDirty("none");
    setChildDiscountDirty("none");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialBase, initialChild, initialDiscount, initialChildDiscount,
    initialDiscountPct, initialChildDiscountPct,
    discount_enabled, child_discount_enabled]);

  // Sync: percent changed → compute price
  useEffect(() => {
    if (discountDirty !== "percent") return;
    const newDiscount = computePriceFromPercent(basePrice, discountPct);
    setDiscountPrice(newDiscount);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [discountPct, discountDirty]);

  // Sync: price changed → compute percent
  useEffect(() => {
    if (discountDirty !== "price") return;
    const newPct = computePercentFromPrice(basePrice, discountPrice);
    setDiscountPct(newPct);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [discountPrice, discountDirty]);

  // Sync: child percent changed → compute child price
  useEffect(() => {
    if (childDiscountDirty !== "percent") return;
    const newPrice = computePriceFromPercent(childPrice, childDiscountPct);
    setChildDiscountPrice(newPrice);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [childDiscountPct, childDiscountDirty]);

  // Sync: child price changed → compute child percent
  useEffect(() => {
    if (childDiscountDirty !== "price") return;
    const newPct = computePercentFromPrice(childPrice, childDiscountPrice);
    setChildDiscountPct(newPct);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [childDiscountPrice, childDiscountDirty]);

  const handleDiscountPctChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = e.target.value.replace(/[^0-9]/g, "");
      const num = Math.min(99, Math.max(0, Number(raw)));
      setDiscountPct(raw === "" ? "" : String(num));
      setDiscountDirty("percent");
    },
    []
  );

  const handleDiscountPriceChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      setDiscountPrice(e.target.value);
      setDiscountDirty("price");
    },
    []
  );

  const handleChildDiscountPctChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = e.target.value.replace(/[^0-9]/g, "");
      const num = Math.min(99, Math.max(0, Number(raw)));
      setChildDiscountPct(raw === "" ? "" : String(num));
      setChildDiscountDirty("percent");
    },
    []
  );

  const handleChildDiscountPriceChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      setChildDiscountPrice(e.target.value);
      setChildDiscountDirty("price");
    },
    []
  );

  const handleBasePriceChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      setBasePrice(e.target.value);
      // Recompute discount pct from existing discount price
      if (discountEnabled && discountPrice) {
        const newPct = computePercentFromPrice(e.target.value, discountPrice);
        setDiscountPct(newPct);
      }
    },
    [discountEnabled, discountPrice]
  );

  const handleChildPriceChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      setChildPrice(e.target.value);
      if (childDiscountEnabled && childDiscountPrice) {
        const newPct = computePercentFromPrice(
          e.target.value,
          childDiscountPrice
        );
        setChildDiscountPct(newPct);
      }
    },
    [childDiscountEnabled, childDiscountPrice]
  );

  return (
    <FormSection title="Pricing & Discount">
      <div className="grid gap-5 md:grid-cols-[1fr_340px]">
        {/* LEFT: base price, discount checkbox, child price, child discount checkbox */}
        <div className="space-y-5">
          <label className="block">
            <span className="mb-2 block text-xs font-bold uppercase tracking-[0.08em] text-[#8a8f9d]">
              Base Price
            </span>
            <input
              name="base_price"
              value={basePrice}
              onChange={handleBasePriceChange}
              className="mt-2 h-10 w-full rounded-md border border-[#e6dfe5] bg-white px-3 text-sm outline-none placeholder:text-[#9da2ad]"
              placeholder="0.00"
            />
          </label>

          <Checkbox
            name="discount_enabled"
            label="Enable Discount"
            checked={discountEnabled}
            onChange={(e) => setDiscountEnabled(e.target.checked)}
          />

          <label className="block">
            <span className="mb-2 block text-xs font-bold uppercase tracking-[0.08em] text-[#8a8f9d]">
              Child Pricing
            </span>
            <input
              name="child_price"
              value={childPrice}
              onChange={handleChildPriceChange}
              className="mt-2 h-10 w-full rounded-md border border-[#e6dfe5] bg-white px-3 text-sm outline-none placeholder:text-[#9da2ad]"
              placeholder="0.00"
            />
          </label>

          <Checkbox
            name="child_discount_enabled"
            label="Enable Discount"
            checked={childDiscountEnabled}
            onChange={(e) => setChildDiscountEnabled(e.target.checked)}
          />
        </div>

        {/* RIGHT: discount price + percent (conditionally shown) */}
        <div className="rounded-xl bg-[#f4f7ff] p-5 space-y-4">
          {discountEnabled && (
            <>
              {/* Discount Price */}
              <label className="block">
                <span className="mb-2 block text-xs font-bold uppercase tracking-[0.08em] text-[#8a8f9d]">
                  Discount Price
                </span>
                <input
                  name="discount_price"
                  value={discountPrice}
                  onChange={handleDiscountPriceChange}
                  className="mt-2 h-10 w-full rounded-md border border-[#e6dfe5] bg-white px-3 text-sm outline-none placeholder:text-[#9da2ad]"
                  placeholder="0.00"
                />
              </label>

              {/* Discount Percent */}
              <label className="block">
                <span className="mb-2 block text-xs font-bold uppercase tracking-[0.08em] text-[#8a8f9d]">
                  Discount (%)
                </span>
                <div className="flex items-center gap-2">
                  <input
                    type="text"
                    inputMode="numeric"
                    value={discountPct}
                    onChange={handleDiscountPctChange}
                    className="h-10 w-20 rounded-md border border-[#e6dfe5] bg-white px-3 text-sm text-right outline-none placeholder:text-[#9da2ad]"
                    placeholder="0"
                  />
                  <span className="text-sm font-bold text-[#707684]">%</span>
                </div>
                {discountPct && (
                  <p className="mt-1 text-xs text-[#0187a9]">
                    {discountPct}% off = {discountPrice || "—"}
                  </p>
                )}
              </label>
            </>
          )}

          {childDiscountEnabled && (
            <>
              {/* Child Discount Price */}
              <label className="block">
                <span className="mb-2 block text-xs font-bold uppercase tracking-[0.08em] text-[#8a8f9d]">
                  Child Discount Price
                </span>
                <input
                  name="child_discount_price"
                  value={childDiscountPrice}
                  onChange={handleChildDiscountPriceChange}
                  className="mt-2 h-10 w-full rounded-md border border-[#e6dfe5] bg-white px-3 text-sm outline-none placeholder:text-[#9da2ad]"
                  placeholder="0.00"
                />
              </label>

              {/* Child Discount Percent */}
              <label className="block">
                <span className="mb-2 block text-xs font-bold uppercase tracking-[0.08em] text-[#8a8f9d]">
                  Child Discount (%)
                </span>
                <div className="flex items-center gap-2">
                  <input
                    type="text"
                    inputMode="numeric"
                    value={childDiscountPct}
                    onChange={handleChildDiscountPctChange}
                    className="h-10 w-20 rounded-md border border-[#e6dfe5] bg-white px-3 text-sm text-right outline-none placeholder:text-[#9da2ad]"
                    placeholder="0"
                  />
                  <span className="text-sm font-bold text-[#707684]">%</span>
                </div>
                {childDiscountPct && (
                  <p className="mt-1 text-xs text-[#0187a9]">
                    {childDiscountPct}% off = {childDiscountPrice || "—"}
                  </p>
                )}
              </label>
            </>
          )}
        </div>
      </div>
    </FormSection>
  );
}
