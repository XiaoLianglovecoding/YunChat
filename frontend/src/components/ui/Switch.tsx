import { useId } from "react";
import { cn } from "../../lib/cn";

export interface SwitchProps {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  label: string;
  description?: string;
  disabled?: boolean;
}

export function Switch({ checked, onCheckedChange, label, description, disabled }: SwitchProps) {
  const descriptionId = useId();

  return (
    <div className={cn("ui-switch-row", disabled && "is-disabled")}>
      <span>
        <span className="ui-switch-row__label">{label}</span>
        {description && <span className="ui-switch-row__description" id={descriptionId}>{description}</span>}
      </span>
      <button
        aria-describedby={description ? descriptionId : undefined}
        aria-label={label}
        aria-checked={checked}
        className={cn("ui-switch", checked && "is-checked")}
        disabled={disabled}
        onClick={() => onCheckedChange(!checked)}
        role="switch"
        type="button"
      >
        <span />
      </button>
    </div>
  );
}
