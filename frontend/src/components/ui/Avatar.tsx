import { useEffect, useState } from "react";
import { cn } from "../../lib/cn";
import { env } from "../../config/env";

type AvatarSize = "sm" | "md" | "lg" | "xl";

export interface AvatarProps {
  src?: string;
  name: string;
  size?: AvatarSize;
  online?: boolean;
  className?: string;
}

function initials(name: string) {
  return name.trim().slice(0, 2).toUpperCase() || "?";
}

export function Avatar({ src, name, size = "md", online, className }: AvatarProps) {
  const resolvedSrc = src ? (src.startsWith("http://") || src.startsWith("https://") || src.startsWith("blob:") || src.startsWith("data:") ? src : `${env.staticBaseUrl}${src.startsWith("/") ? "" : "/"}${src}`) : undefined;
  const [failedSrc, setFailedSrc] = useState<string>();

  useEffect(() => setFailedSrc(undefined), [resolvedSrc]);

  return (
    <span className={cn("ui-avatar", `ui-avatar--${size}`, className)} aria-label={name} role="img">
      {resolvedSrc && failedSrc !== resolvedSrc ? <img src={resolvedSrc} alt="" onError={() => setFailedSrc(resolvedSrc)} /> : <span>{initials(name)}</span>}
      {online !== undefined && <i className={cn("ui-avatar__status", online && "is-online")} aria-hidden="true" />}
    </span>
  );
}
