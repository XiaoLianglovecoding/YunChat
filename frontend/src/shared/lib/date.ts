const relativeFormatter = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });

export function relativeTime(value: string | Date, now = new Date()): string {
  const date = typeof value === "string" ? new Date(value) : value;
  const differenceSeconds = Math.round((date.getTime() - now.getTime()) / 1000);
  const absoluteSeconds = Math.abs(differenceSeconds);
  if (absoluteSeconds < 60) return relativeFormatter.format(differenceSeconds, "second");
  if (absoluteSeconds < 3_600) return relativeFormatter.format(Math.round(differenceSeconds / 60), "minute");
  if (absoluteSeconds < 86_400) return relativeFormatter.format(Math.round(differenceSeconds / 3_600), "hour");
  if (absoluteSeconds < 604_800) return relativeFormatter.format(Math.round(differenceSeconds / 86_400), "day");
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit" }).format(date);
}

