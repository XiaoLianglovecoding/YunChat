import { AnimatePresence, motion, useReducedMotion } from "framer-motion";
import { CheckCircle2, CircleAlert, Info } from "lucide-react";

export type ToastTone = "success" | "error" | "info";

export interface ToastItem {
  id: string;
  message: string;
  tone?: ToastTone;
}

interface ToastViewportProps {
  items: ToastItem[];
}

const icons = {
  success: CheckCircle2,
  error: CircleAlert,
  info: Info,
};

export function ToastViewport({ items }: ToastViewportProps) {
  const reduceMotion = useReducedMotion();

  return (
    <div aria-live="polite" className="ui-toast-viewport">
      <AnimatePresence initial={false}>
        {items.map((item) => {
          const tone = item.tone ?? "info";
          const Icon = icons[tone];
          return (
            <motion.div
              className={`ui-toast ui-toast--${tone}`}
              initial={{ y: reduceMotion ? 0 : -8, opacity: 0 }}
              animate={{ y: 0, opacity: 1 }}
              exit={{ y: reduceMotion ? 0 : -6, opacity: 0 }}
              key={item.id}
              role={tone === "error" ? "alert" : "status"}
            >
              <Icon aria-hidden="true" size={17} />
              <span>{item.message}</span>
            </motion.div>
          );
        })}
      </AnimatePresence>
    </div>
  );
}
