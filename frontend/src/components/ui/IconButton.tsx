import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "../../lib/cn";

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string;
  children: ReactNode;
  selected?: boolean;
}

export function IconButton({ label, children, selected, className, ...props }: IconButtonProps) {
  return (
    <button
      aria-label={label}
      aria-pressed={selected}
      className={cn("ui-icon-button", selected && "is-selected", className)}
      title={label}
      {...props}
    >
      {children}
    </button>
  );
}
