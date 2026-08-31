import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from "react";
import { cn } from "../../lib/cn";

export interface TextFieldProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  hint?: string;
  error?: string;
  leadingIcon?: ReactNode;
}

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField(
  { id, label, hint, error, leadingIcon, className, ...props },
  ref,
) {
  const generatedId = useId();
  const fieldId = id ?? generatedId;
  const descriptionId = `${fieldId}-description`;

  return (
    <label className="ui-field" htmlFor={fieldId}>
      {label && <span className="ui-field__label">{label}</span>}
      <span className={cn("ui-field__control", error && "has-error")}>
        {leadingIcon && <span className="ui-field__icon">{leadingIcon}</span>}
        <input
          ref={ref}
          id={fieldId}
          aria-describedby={hint || error ? descriptionId : undefined}
          aria-invalid={Boolean(error)}
          className={cn("ui-field__input", className)}
          {...props}
        />
      </span>
      {(error || hint) && (
        <span className={cn("ui-field__hint", error && "has-error")} id={descriptionId}>
          {error ?? hint}
        </span>
      )}
    </label>
  );
});
