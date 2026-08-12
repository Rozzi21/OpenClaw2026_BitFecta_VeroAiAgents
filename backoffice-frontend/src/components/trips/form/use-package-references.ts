"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { apiFetch, TripPackage } from "@/lib/api";

export type PackageReference = { id: string; title: string };

const SEARCH_DEBOUNCE_MS = 400;
const MIN_QUERY_LENGTH = 2;
const SEARCH_RESULT_LIMIT = 8;
const LOOKUP_LIMIT = 200;

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isTripId(value: string): boolean {
  return UUID_PATTERN.test(value.trim());
}

type SearchState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "success"; results: TripPackage[] };

async function fetchPackages(search: string, limit: number, signal?: AbortSignal) {
  const params = new URLSearchParams({ limit: String(limit) });
  if (search) {
    params.set("search", search);
  }
  return apiFetch<TripPackage[]>(
    `/api/v1/admin/packages?${params.toString()}`,
    { signal },
    true
  );
}

export function usePackageReferences(
  initialReferences: string[] | undefined,
  formKey: string
) {
  const [selected, setSelected] = useState<PackageReference[]>([]);
  const [query, setQuery] = useState("");
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const [searchState, setSearchState] = useState<SearchState>({ status: "idle" });

  const selectedRef = useRef<PackageReference[]>([]);
  const abortRef = useRef<AbortController | null>(null);
  const requestIdRef = useRef(0);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    selectedRef.current = selected;
  }, [selected]);

  // Resolve initial references (trip IDs from the loaded trip) into
  // {id, title} cards. Non-ID legacy values (plain titles) are dropped.
  useEffect(() => {
    const ids = (initialReferences ?? []).map((item) => item.trim()).filter(isTripId);
    setQuery("");
    setDropdownOpen(false);
    setSearchState({ status: "idle" });
    abortRef.current?.abort();

    if (ids.length === 0) {
      setSelected([]);
      return;
    }

    let cancelled = false;
    fetchPackages("", LOOKUP_LIMIT)
      .then((packages) => {
        if (cancelled) {
          return;
        }
        const titleById = new Map(packages.map((pkg) => [pkg.id, pkg.title]));
        setSelected(
          ids.map((id) => ({ id, title: titleById.get(id) ?? "Paket tidak ditemukan" }))
        );
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setSelected(ids.map((id) => ({ id, title: "Paket referensi" })));
      });

    return () => {
      cancelled = true;
    };
    // Re-run only when switching between create/edit form instances.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formKey]);

  const runSearch = useCallback((value: string) => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const requestId = ++requestIdRef.current;

    setSearchState({ status: "loading" });
    fetchPackages(value, SEARCH_RESULT_LIMIT, controller.signal)
      .then((packages) => {
        if (controller.signal.aborted || requestId !== requestIdRef.current) {
          return;
        }
        const selectedIds = new Set(selectedRef.current.map((item) => item.id));
        setSearchState({
          status: "success",
          results: packages.filter((pkg) => !selectedIds.has(pkg.id)),
        });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || requestId !== requestIdRef.current) {
          return;
        }
        if (error instanceof Error && error.name === "AbortError") {
          return;
        }
        setSearchState({
          status: "error",
          message:
            error instanceof Error ? error.message : "Gagal mencari paket.",
        });
      });
  }, []);

  const handleQueryChange = useCallback(
    (value: string) => {
      setQuery(value);
      setDropdownOpen(true);

      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
        debounceRef.current = null;
      }

      const trimmed = value.trim();
      if (trimmed.length < MIN_QUERY_LENGTH) {
        abortRef.current?.abort();
        setSearchState({ status: "idle" });
        return;
      }

      debounceRef.current = setTimeout(() => runSearch(trimmed), SEARCH_DEBOUNCE_MS);
    },
    [runSearch]
  );

  const selectPackage = useCallback((pkg: PackageReference) => {
    setSelected((items) =>
      items.some((item) => item.id === pkg.id) ? items : [...items, pkg]
    );
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
      debounceRef.current = null;
    }
    abortRef.current?.abort();
    setQuery("");
    setDropdownOpen(false);
    setSearchState({ status: "idle" });
  }, []);

  const removePackage = useCallback((id: string) => {
    setSelected((items) => items.filter((item) => item.id !== id));
  }, []);

  const closeDropdown = useCallback(() => setDropdownOpen(false), []);

  // Cancel pending debounce/request on unmount.
  useEffect(() => {
    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
      abortRef.current?.abort();
    };
  }, []);

  return {
    selected,
    query,
    dropdownOpen,
    searchState,
    handleQueryChange,
    selectPackage,
    removePackage,
    closeDropdown,
  };
}
