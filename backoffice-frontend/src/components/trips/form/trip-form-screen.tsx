"use client";

import { useState } from "react";
import Link from "next/link";
import { ToastNotification } from "@/components/toast-notification";
import { InfoModal } from "../list/info-modal";
import { ModalType } from "../list/types";
import { BackofficeSidebar } from "../shared/backoffice-sidebar";
import { useTripForm } from "./use-trip-form";
import { TripFormFooter } from "./trip-form-footer";
import { BasicInfoSection } from "./sections/basic-info-section";
import { MediaSection } from "./sections/media-section";
import { SummarySection } from "./sections/summary-section";
import { AmenitiesSection } from "./sections/amenities-section";
import { ItinerarySection } from "./sections/itinerary-section";
import { HighlightsSection } from "./sections/highlights-section";
import { PricingSection } from "./sections/pricing-section";
import { SchedulingSection } from "./sections/scheduling-section";
import { ReferenceSection } from "./sections/reference-section";

export function TripFormScreen() {
  const form = useTripForm();
  const defaults = form.staticDefaults;
  const [modal, setModal] = useState<ModalType>(null);

  if (form.loadingTrip) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-[#fbfaff] text-sm font-bold text-[#6f7480]">
        Memuat data trip...
      </main>
    );
  }

  if (form.loadError) {
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-4 bg-[#fbfaff] px-6 text-center">
        <p className="text-sm font-bold text-[#e9272e]">{form.loadError}</p>
        <Link
          href="/"
          className="rounded-lg bg-[#e9272e] px-5 py-2 text-xs font-bold text-white"
        >
          Kembali ke daftar trip
        </Link>
      </main>
    );
  }

  return (
    <div className="min-h-screen bg-[#fbfaff] text-[#171923]">
      {form.toast && (
        <ToastNotification toast={form.toast} onClose={() => form.setToast(null)} />
      )}
      <InfoModal type={modal} onClose={() => setModal(null)} />
      <BackofficeSidebar mode="form" onModalOpen={setModal} />

      <main className="pb-28 lg:pl-[288px]">
        <div className="mx-auto max-w-[760px] px-6 py-10">
          <header>
            <h1 className="text-3xl font-extrabold tracking-[-0.04em]">
              {form.isEditMode ? "Edit Trip" : "Create New Trip"}
            </h1>
            <p className="mt-2 text-sm text-[#707684]">
              {form.isEditMode
                ? "Perbarui konfigurasi paket travel yang sudah ada."
                : "Design and configure a new travel package for the operating system."}
            </p>
          </header>

          <form
            id="trip-form"
            key={form.formKey}
            className="mt-9 space-y-8"
            onSubmit={form.handleSubmit}
          >
            <BasicInfoSection
              basicInfo={form.basicInfo}
              title={defaults.title}
              location={defaults.location}
            />
            <MediaSection media={form.media} />
            <SummarySection defaultValue={defaults.summary} />
            <AmenitiesSection amenities={form.amenities} />
            <ItinerarySection itinerary={form.itinerary} />
            <HighlightsSection highlights={form.highlights} />
            <PricingSection
              base_price={defaults.base_price}
              child_price={defaults.child_price}
              discount_price={defaults.discount_price}
              child_discount_price={defaults.child_discount_price}
              discount_percent={defaults.discount_percent}
              child_discount_percent={defaults.child_discount_percent}
              discount_enabled={defaults.discount_enabled}
              child_discount_enabled={defaults.child_discount_enabled}
            />
            <SchedulingSection
              scheduling={form.scheduling}
              package_start={defaults.package_start}
              package_end={defaults.package_end}
              publish_start={defaults.publish_start}
              publish_end={defaults.publish_end}
            />
            <ReferenceSection defaultValue={defaults.reference} />
          </form>
        </div>
      </main>

      <TripFormFooter
        saving={form.saving}
        isEditMode={form.isEditMode}
        submitStatus={form.submitStatus}
        onDraftClick={form.setDraftSubmit}
        onPublishClick={form.setPublishedSubmit}
      />
    </div>
  );
}
