import { cn } from "../lib/cn.ts";

type AvatarProps = {
  initials: string;
  color?: "green" | "coral" | "gold" | "ink";
  online?: boolean;
  size?: "small" | "medium" | "large";
};

export function Avatar({ initials, color = "ink", online = false, size = "medium" }: AvatarProps) {
  return (
    <span className={cn("avatar", `avatar--${color}`, `avatar--${size}`)} aria-label={initials}>
      {initials.slice(0, 2)}
      {online ? <span className="avatar__presence" aria-label="在线" /> : null}
    </span>
  );
}

