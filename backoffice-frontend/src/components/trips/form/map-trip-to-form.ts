import { TripPackage } from "@/lib/api";
import {
  INITIAL_AMENITIES,
  INITIAL_ITINERARIES,
  ItineraryItem,
  ScheduleType,
  TripCategory,
  UploadedMedia,
} from "./types";

export type TripFormStaticDefaults = {
  title: string;
  location: string;
  summary: string;
  base_price: string;
  child_price: string;
  discount_price: string;
  child_discount_price: string;
  discount_percent: string;
  child_discount_percent: string;
  discount_enabled: boolean;
  child_discount_enabled: boolean;
  package_start: string;
  package_end: string;
  publish_start: string;
  publish_end: string;
};


export type TripFormControlledState = {
  category: TripCategory;
  scheduleType: ScheduleType;
  visibilityEnabled: boolean;
  uploadedMedia: UploadedMedia[];
  amenitiesIncluded: string[];
  amenitiesExcluded: string[];
  itineraries: ItineraryItem[];
  highlights: string[];
  durationDays: string;
  durationNights: string;
  adultPaxEnabled: boolean;
  childPaxEnabled: boolean;
  adultPax: string;
  childPax: string;
};

export function computeDiscountPercent(
  basePrice: number,
  discountPrice: number
): string {
  if (basePrice <= 0 || discountPrice <= 0 || discountPrice >= basePrice) {
    return "";
  }
  return String(Math.round(((basePrice - discountPrice) / basePrice) * 100));
}

export function parseDuration(duration: string): { days: string; nights: string } {
  const match = duration.match(/(\d+)\s*Days?\s*\/\s*(\d+)\s*Nights?/i);
  if (match) {
    return { days: match[1], nights: match[2] };
  }
  return { days: "", nights: "" };
}

export function formatDateForInput(value?: string): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toISOString().slice(0, 10);
}

function normalizeCategory(category: string): TripCategory {
  return category === "local" ? "local" : "international";
}

function normalizeScheduleType(value?: string): ScheduleType {
  return value === "flexible" ? "flexible" : "date_range";
}

function normalizeAmenities(items?: string[]): string[] {
  if (!items?.length) {
    return INITIAL_AMENITIES;
  }
  return items;
}

function normalizeItineraries(items?: ItineraryItem[]): ItineraryItem[] {
  if (!items?.length) {
    return INITIAL_ITINERARIES;
  }
  return items.map((item, index) => ({
    day: item.day || index + 1,
    title: item.title ?? "",
    description: item.description ?? "",
  }));
}

export function mapTripToForm(trip: TripPackage): {
  controlled: TripFormControlledState;
  defaults: TripFormStaticDefaults;
} {
  const { days, nights } = parseDuration(trip.duration);
  const publishStart = formatDateForInput(trip.publish_start_date);
  const publishEnd = formatDateForInput(trip.publish_end_date);
  const adultPax = trip.adult_pax ?? 0;
  const childPax = trip.child_pax ?? 0;

  return {
    controlled: {
      category: normalizeCategory(trip.category),
      scheduleType: normalizeScheduleType(trip.schedule_type),
      visibilityEnabled: Boolean(publishStart && publishEnd),
      uploadedMedia: (trip.media ?? []).map((item) => ({
        url: item.url,
        type: item.type || "image",
      })),
      amenitiesIncluded: normalizeAmenities(trip.amenities_included),
      amenitiesExcluded: normalizeAmenities(trip.amenities_excluded),
      itineraries: normalizeItineraries(trip.itineraries),
      highlights: trip.highlights ?? [],
      durationDays: days,
      durationNights: nights,
      adultPaxEnabled: adultPax > 0,
      childPaxEnabled: childPax > 0,
      adultPax: adultPax > 0 ? String(adultPax) : "",
      childPax: childPax > 0 ? String(childPax) : "",
    },
    defaults: {
      title: trip.title,
      location: trip.location,
      summary: trip.summary || trip.overview || "",
      base_price: String(trip.base_price ?? ""),
      child_price: String(trip.child_price ?? ""),
      discount_price: String(trip.discount_price ?? ""),
      child_discount_price: String(trip.child_discount_price ?? ""),
      discount_percent: computeDiscountPercent(
        trip.base_price ?? 0,
        trip.discount_price ?? 0
      ),
      child_discount_percent: computeDiscountPercent(
        trip.child_price ?? 0,
        trip.child_discount_price ?? 0
      ),
      discount_enabled: trip.discount_enabled ?? false,
      child_discount_enabled: trip.child_discount_enabled ?? false,
      package_start: formatDateForInput(trip.package_start_date),
      package_end: formatDateForInput(trip.package_end_date),
      publish_start: publishStart,
      publish_end: publishEnd,
    },

  };
}
