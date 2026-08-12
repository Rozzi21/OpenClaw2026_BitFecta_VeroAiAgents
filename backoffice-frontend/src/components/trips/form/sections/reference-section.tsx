import { useRef } from "react";
import { FormSection } from "../ui/form-section";
import { UseTripFormReturn } from "../use-trip-form";

type Props = Pick<UseTripFormReturn, "references">;

export function ReferenceSection({ references }: Props) {
  const {
    selected,
    query,
    dropdownOpen,
    searchState,
    handleQueryChange,
    selectPackage,
    removePackage,
    closeDropdown,
  } = references;
  const containerRef = useRef<HTMLDivElement>(null);

  const trimmedQuery = query.trim();
  const showDropdown = dropdownOpen && trimmedQuery.length >= 2;

  return (
    <FormSection title="Other Package Reference">
      <p className="text-xs text-[#7d838d]">
        Add reference packages so this content finds alternatives if this package is a
        perfect match.
      </p>

      <div
        ref={containerRef}
        className="relative"
        onBlur={(event) => {
          if (!containerRef.current?.contains(event.relatedTarget as Node)) {
            closeDropdown();
          }
        }}
      >
        <input
          value={query}
          onChange={(event) => handleQueryChange(event.target.value)}
          onFocus={() => handleQueryChange(query)}
          className="h-10 w-full rounded-md border border-[#e6dfe5] bg-white px-3 text-sm outline-none"
          placeholder="Search package title..."
          autoComplete="off"
        />

        {showDropdown && (
          <div className="absolute inset-x-0 top-full z-20 mt-1 overflow-hidden rounded-md border border-[#e6dfe5] bg-white shadow-lg">
            {searchState.status === "loading" && (
              <p className="px-3 py-3 text-xs font-semibold text-[#8a8f9d]">
                Mencari paket...
              </p>
            )}
            {searchState.status === "error" && (
              <p className="px-3 py-3 text-xs font-semibold text-[#e9272e]">
                {searchState.message}
              </p>
            )}
            {searchState.status === "success" &&
              (searchState.results.length === 0 ? (
                <p className="px-3 py-3 text-xs font-semibold text-[#8a8f9d]">
                  Tidak ada paket ditemukan.
                </p>
              ) : (
                <ul className="max-h-56 overflow-y-auto py-1">
                  {searchState.results.map((pkg) => (
                    <li key={pkg.id}>
                      <button
                        type="button"
                        onClick={() =>
                          selectPackage({ id: pkg.id, title: pkg.title })
                        }
                        className="block w-full px-3 py-2 text-left text-sm text-[#171923] hover:bg-[#f6edf0]"
                      >
                        {pkg.title}
                      </button>
                    </li>
                  ))}
                </ul>
              ))}
          </div>
        )}
      </div>

      {selected.length > 0 && (
        <ul className="space-y-2">
          {selected.map((pkg) => (
            <li
              key={pkg.id}
              className="flex items-center justify-between rounded-md border border-[#e6dfe5] bg-white px-3 py-2"
            >
              <span className="text-sm font-semibold text-[#171923]">
                {pkg.title}
              </span>
              <button
                type="button"
                onClick={() => removePackage(pkg.id)}
                aria-label={`Hapus ${pkg.title}`}
                className="ml-3 text-base font-bold leading-none text-[#8a8f9d] hover:text-[#e9272e]"
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
    </FormSection>
  );
}
