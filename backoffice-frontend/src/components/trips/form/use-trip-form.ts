"use client";

import { FormEvent, useEffect, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { apiFetch, getToken, TripPackage } from "@/lib/api";
import { buildTripFormSubmitPayload, fetchTripDetail } from "@/lib/trip";
import { ToastState } from "@/components/toast-notification";
import {
  mapTripToForm,
  TripFormStaticDefaults,
} from "./map-trip-to-form";
import {
  INITIAL_AMENITIES,
  INITIAL_HIGHLIGHTS,
  INITIAL_ITINERARIES,
  ItineraryItem,
  ScheduleType,
  SubmitStatus,
  TripCategory,
  UploadedMedia,
} from "./types";
import { invalidateTripsCache } from "../list/use-trips-list";
import { usePackageReferences } from "./use-package-references";

const EMPTY_DEFAULTS: TripFormStaticDefaults = {

  title: "",
  location: "",
  summary: "",
  base_price: "",
  child_price: "",
  discount_price: "",
  child_discount_price: "",
  discount_percent: "",
  child_discount_percent: "",
  discount_enabled: false,
  child_discount_enabled: false,
  package_start: "",
  package_end: "",
  publish_start: "",
  publish_end: "",
};


function applyControlledState(
  controlled: ReturnType<typeof mapTripToForm>["controlled"],
  setters: {
    setCategory: (value: TripCategory) => void;
    setScheduleType: (value: ScheduleType) => void;
    setVisibilityEnabled: (value: boolean) => void;
    setUploadedMedia: (value: UploadedMedia[]) => void;
    setAmenitiesIncluded: (value: string[]) => void;
    setAmenitiesExcluded: (value: string[]) => void;
    setItineraries: (value: ItineraryItem[]) => void;
    setHighlights: (value: string[]) => void;
    setDurationDays: (value: string) => void;
    setDurationNights: (value: string) => void;
    setAdultPaxEnabled: (value: boolean) => void;
    setChildPaxEnabled: (value: boolean) => void;
    setAdultPax: (value: string) => void;
    setChildPax: (value: string) => void;
  }
) {
  setters.setCategory(controlled.category);
  setters.setScheduleType(controlled.scheduleType);
  setters.setVisibilityEnabled(controlled.visibilityEnabled);
  setters.setUploadedMedia(controlled.uploadedMedia);
  setters.setAmenitiesIncluded(controlled.amenitiesIncluded);
  setters.setAmenitiesExcluded(controlled.amenitiesExcluded);
  setters.setItineraries(controlled.itineraries);
  setters.setHighlights(controlled.highlights);
  setters.setDurationDays(controlled.durationDays);
  setters.setDurationNights(controlled.durationNights);
  setters.setAdultPaxEnabled(controlled.adultPaxEnabled);
  setters.setChildPaxEnabled(controlled.childPaxEnabled);
  setters.setAdultPax(controlled.adultPax);
  setters.setChildPax(controlled.childPax);
}

export function useTripForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const editId = searchParams.get("edit");

  const [category, setCategory] = useState<TripCategory>("local");
  const [scheduleType, setScheduleType] = useState<ScheduleType>("date_range");
  const [visibilityEnabled, setVisibilityEnabled] = useState(false);
  const [uploadedMedia, setUploadedMedia] = useState<UploadedMedia[]>([]);
  const [amenitiesIncluded, setAmenitiesIncluded] = useState(INITIAL_AMENITIES);
  const [amenitiesExcluded, setAmenitiesExcluded] = useState(INITIAL_AMENITIES);
  const [itineraries, setItineraries] = useState<ItineraryItem[]>(INITIAL_ITINERARIES);
  const [highlights, setHighlights] = useState(INITIAL_HIGHLIGHTS);
  const [highlightInput, setHighlightInput] = useState("");
  const [durationDays, setDurationDays] = useState("");
  const [durationNights, setDurationNights] = useState("");
  const [adultPaxEnabled, setAdultPaxEnabled] = useState(false);
  const [childPaxEnabled, setChildPaxEnabled] = useState(false);
  const [adultPax, setAdultPax] = useState("");
  const [childPax, setChildPax] = useState("");
  const [toast, setToast] = useState<ToastState | null>(null);
  const [saving, setSaving] = useState(false);
  const [loadingTrip, setLoadingTrip] = useState(Boolean(editId));
  const [loadError, setLoadError] = useState("");
  const [loadedTrip, setLoadedTrip] = useState<TripPackage | null>(null);
  const [staticDefaults, setStaticDefaults] =
    useState<TripFormStaticDefaults>(EMPTY_DEFAULTS);
  const [formKey, setFormKey] = useState(editId ?? "new");
  const submitStatus = useRef<SubmitStatus>("draft");
  const references = usePackageReferences(loadedTrip?.references, formKey, editId);



  useEffect(() => {
    if (!getToken()) {
      setToast({
        type: "info",
        text: "Silakan login terlebih dahulu untuk upload media dan menyimpan paket.",
      });
    }
  }, []);

  function resetDynamicFields() {
    setCategory("local");
    setScheduleType("date_range");
    setVisibilityEnabled(false);
    setUploadedMedia([]);
    setAmenitiesIncluded(INITIAL_AMENITIES);
    setAmenitiesExcluded(INITIAL_AMENITIES);
    setItineraries(INITIAL_ITINERARIES);
    setHighlights(INITIAL_HIGHLIGHTS);
    setHighlightInput("");
    setDurationDays("");
    setDurationNights("");
    setAdultPaxEnabled(false);
    setChildPaxEnabled(false);
    setAdultPax("");
    setChildPax("");
  }

  useEffect(() => {
    if (!editId) {
      setLoadingTrip(false);
      setLoadError("");
      setLoadedTrip(null);
      setStaticDefaults(EMPTY_DEFAULTS);
      setFormKey("new");
      resetDynamicFields();
      return;
    }

    let cancelled = false;
    setLoadingTrip(true);
    setLoadError("");
    setLoadedTrip(null);

    fetchTripDetail(editId)
      .then((trip) => {
        if (cancelled) {
          return;
        }
        const mapped = mapTripToForm(trip);
        applyControlledState(mapped.controlled, {
          setCategory,
          setScheduleType,
          setVisibilityEnabled,
          setUploadedMedia,
          setAmenitiesIncluded,
          setAmenitiesExcluded,
          setItineraries,
          setHighlights,
          setDurationDays,
          setDurationNights,
          setAdultPaxEnabled,
          setChildPaxEnabled,
          setAdultPax,
          setChildPax,
        });
        setStaticDefaults(mapped.defaults);
        setLoadedTrip(trip);
        setFormKey(editId);
        setLoadingTrip(false);
      })
      .catch((error) => {
        if (cancelled) {
          return;
        }
        setLoadError(
          error instanceof Error ? error.message : "Gagal memuat data trip."
        );
        setLoadingTrip(false);
      });

    return () => {
      cancelled = true;
    };
  }, [editId]);

  async function handleUpload(file?: File) {
    if (!file) {
      return;
    }
    if (uploadedMedia.length >= 5) {
      setToast({ type: "error", text: "Maksimal 5 gambar untuk satu trip." });
      return;
    }
    setToast({ type: "info", text: "Mengupload gambar..." });
    try {
      const body = new FormData();
      body.append("file", file);
      const uploaded = await apiFetch<{ url: string }>(
        "/api/v1/admin/uploads",
        { method: "POST", body },
        true
      );
      setUploadedMedia((items) =>
        [...items, { url: uploaded.url, type: "image" }].slice(0, 5)
      );
      setToast({ type: "success", text: "Gambar berhasil diupload." });
    } catch (error) {
      setToast({
        type: "error",
        text: error instanceof Error ? error.message : "Gagal upload gambar",
      });
    }
  }

  function removeMedia(url: string) {
    setUploadedMedia((items) => items.filter((item) => item.url !== url));
  }

  function updateAmenity(
    type: "included" | "excluded",
    index: number,
    value: string
  ) {
    const setter =
      type === "included" ? setAmenitiesIncluded : setAmenitiesExcluded;
    setter((items) =>
      items.map((item, itemIndex) => (itemIndex === index ? value : item))
    );
  }

  function addAmenity(type: "included" | "excluded") {
    const setter =
      type === "included" ? setAmenitiesIncluded : setAmenitiesExcluded;
    setter((items) => [...items, ""]);
  }

  function removeAmenity(type: "included" | "excluded", index: number) {
    const setter =
      type === "included" ? setAmenitiesIncluded : setAmenitiesExcluded;
    setter((items) => {
      if (items.length === 1) {
        return [""];
      }
      return items.filter((_, itemIndex) => itemIndex !== index);
    });
  }

  function updateItinerary(
    index: number,
    field: "title" | "description",
    value: string
  ) {
    setItineraries((items) =>
      items.map((item, itemIndex) =>
        itemIndex === index ? { ...item, [field]: value } : item
      )
    );
  }

  function addItinerary() {
    setItineraries((items) => [
      ...items,
      { day: items.length + 1, title: "", description: "" },
    ]);
  }

  function removeItinerary(index: number) {
    setItineraries((items) => {
      if (items.length === 1) {
        return INITIAL_ITINERARIES;
      }
      return items
        .filter((_, itemIndex) => itemIndex !== index)
        .map((item, itemIndex) => ({ ...item, day: itemIndex + 1 }));
    });
  }

  function addHighlight(value: string) {
    const cleanValue = value.trim();
    if (!cleanValue || highlights.includes(cleanValue)) {
      setHighlightInput("");
      return;
    }
    setHighlights((items) => [...items, cleanValue]);
    setHighlightInput("");
  }

  function removeHighlight(highlight: string) {
    setHighlights((items) => items.filter((item) => item !== highlight));
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const title = String(form.get("title") || "").trim();
    const location = String(form.get("location") || "").trim();
    const days = Number(durationDays || 0);
    const nights = Number(durationNights || 0);
    const duration =
      days > 0 || nights > 0 ? `${days} Days / ${nights} Nights` : "";
    const summary = String(form.get("summary") || "").trim();
    const basePrice = Number(form.get("base_price") ?? 0);
    const basePriceRaw = String(form.get("base_price") ?? "").trim();
    const packageStartDate = String(form.get("package_start") || "").trim();
    const packageEndDate = String(form.get("package_end") || "").trim();
    const publishStartDate = visibilityEnabled
      ? String(form.get("publish_start") || "").trim()
      : "";
    const publishEndDate = visibilityEnabled
      ? String(form.get("publish_end") || "").trim()
      : "";
    const adultPaxCount = adultPaxEnabled ? Number(adultPax || 0) : 0;
    const childPaxCount = childPaxEnabled ? Number(childPax || 0) : 0;
    const incompleteItinerary = itineraries.some(
      (item) =>
        (item.title.trim() && !item.description.trim()) ||
        (!item.title.trim() && item.description.trim())
    );
    const cleanedHighlights = highlights.map((item) => item.trim()).filter(Boolean);

    if (!getToken()) {
      setToast({
        type: "error",
        text: "Silakan login terlebih dahulu sebelum menyimpan trip.",
      });
      return;
    }
    if (!title || !location || !duration || !summary || basePriceRaw === "") {
      setToast({
        type: "error",
        text: "Lengkapi field wajib: Trip Name, Location, Duration, Summary, dan Base Price.",
      });
      return;
    }
    if (
      (adultPaxEnabled && adultPaxCount <= 0) ||
      (childPaxEnabled && childPaxCount <= 0)
    ) {
      setToast({
        type: "error",
        text: "Jumlah pax wajib lebih dari 0 untuk tipe pax yang diaktifkan.",
      });
      return;
    }
    if (scheduleType === "date_range" && (!packageStartDate || !packageEndDate)) {
      setToast({
        type: "error",
        text: "Package Dates wajib diisi untuk schedule Date Range.",
      });
      return;
    }
    if (visibilityEnabled && (!publishStartDate || !publishEndDate)) {
      setToast({
        type: "error",
        text: "Visibility dates wajib diisi jika Add Visibility diaktifkan.",
      });
      return;
    }
    if (cleanedHighlights.length === 0) {
      setToast({
        type: "error",
        text: "Tambahkan minimal satu highlight untuk trip ini.",
      });
      return;
    }
    if (incompleteItinerary) {
      setToast({
        type: "error",
        text: "Lengkapi title dan description pada itinerary, atau kosongkan keduanya.",
      });
      return;
    }

    setSaving(true);
    setToast({ type: "info", text: "Menyimpan paket..." });
    const body = {
      title,
      location,
      destination: location,
      duration,
      adult_pax: adultPaxCount,
      child_pax: childPaxCount,
      category,
      status: submitStatus.current,
      media: uploadedMedia,
      image_url: uploadedMedia[0]?.url || "",
      summary,
      overview: summary,
      amenities_included: amenitiesIncluded.map((item) => item.trim()).filter(Boolean),
      amenities_excluded: amenitiesExcluded.map((item) => item.trim()).filter(Boolean),
      highlights: cleanedHighlights,
      base_price: basePrice,
      child_price: Number(form.get("child_price") || 0),
      discount_price: Number(form.get("discount_price") || 0),
      child_discount_price: Number(form.get("child_discount_price") || 0),
      discount_enabled: form.get("discount_enabled") === "on",
      child_discount_enabled: form.get("child_discount_enabled") === "on",
      schedule_type: scheduleType,
      package_start_date: scheduleType === "date_range" ? packageStartDate : "",
      package_end_date: scheduleType === "date_range" ? packageEndDate : "",
      publish_start_date: publishStartDate,
      publish_end_date: publishEndDate,
      references: references.selected
        .map((item) => item.id)
        .filter((id) => id !== editId),


      itineraries: itineraries
        .map((item, index) => ({
          day: index + 1,
          title: item.title.trim(),
          description: item.description.trim(),
        }))
        .filter((item) => item.title || item.description),
    };

    const isEdit = Boolean(editId && loadedTrip);
    const payload = buildTripFormSubmitPayload(body, loadedTrip ?? undefined);

    try {
      if (isEdit) {
        await apiFetch(
          `/api/v1/admin/packages/${editId}`,
          { method: "PUT", body: JSON.stringify(payload) },
          true
        );
      } else {
        await apiFetch(
          "/api/v1/admin/packages",
          { method: "POST", body: JSON.stringify(payload) },
          true
        );
      }

      const statusLabel =
        submitStatus.current === "published" ? "published" : "draft";
      setToast({
        type: "success",
        text: isEdit
          ? `Trip berhasil diperbarui sebagai ${statusLabel}.`
          : `Trip berhasil ditambahkan sebagai ${statusLabel}.`,
      });
      invalidateTripsCache();

      if (!isEdit) {
        formElement.reset();
        resetDynamicFields();
        setStaticDefaults(EMPTY_DEFAULTS);
        setFormKey("new");
      }

      setTimeout(() => {
        router.push("/");
      }, 1200);
    } catch (error) {
      setToast({
        type: "error",
        text: error instanceof Error ? error.message : "Gagal menyimpan paket",
      });
    } finally {
      setSaving(false);
    }
  }

  function setDraftSubmit() {
    submitStatus.current = "draft";
  }

  function setPublishedSubmit() {
    submitStatus.current = "published";
  }

  return {
    toast,
    setToast,
    saving,
    loadingTrip,
    loadError,
    isEditMode: Boolean(editId),
    formKey,
    staticDefaults,
    submitStatus,
    handleSubmit,
    setDraftSubmit,
    setPublishedSubmit,
    basicInfo: {
      category,
      setCategory,
      durationDays,
      setDurationDays,
      durationNights,
      setDurationNights,
      adultPaxEnabled,
      setAdultPaxEnabled,
      childPaxEnabled,
      setChildPaxEnabled,
      adultPax,
      setAdultPax,
      childPax,
      setChildPax,
    },
    media: {
      uploadedMedia,
      handleUpload,
      removeMedia,
    },
    amenities: {
      amenitiesIncluded,
      amenitiesExcluded,
      addAmenity,
      removeAmenity,
      updateAmenity,
    },
    itinerary: {
      itineraries,
      addItinerary,
      removeItinerary,
      updateItinerary,
    },
    highlights: {
      highlights,
      highlightInput,
      setHighlightInput,
      addHighlight,
      removeHighlight,
    },
    scheduling: {
      scheduleType,
      setScheduleType,
      visibilityEnabled,
      setVisibilityEnabled,
    },
    references,
  };
}


export type UseTripFormReturn = ReturnType<typeof useTripForm>;
