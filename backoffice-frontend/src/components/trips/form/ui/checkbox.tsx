export function Checkbox({
  name,
  label,
  checked,
  onChange,
}: {
  name: string;
  label: string;
  checked?: boolean;
  onChange?: React.ChangeEventHandler<HTMLInputElement>;
}) {
  return (
    <label className="flex items-center gap-2 text-xs font-bold text-[#6f4751]">
      <input
        name={name}
        defaultChecked={onChange ? undefined : checked}
        checked={onChange ? checked : undefined}
        onChange={onChange}
        type="checkbox"
        className="accent-[#e9272e]"
      />
      {label}
    </label>
  );
}
