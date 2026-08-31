import { AnimatePresence, motion, useReducedMotion } from "framer-motion";
import { X } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { Button } from "./Button";
import { IconButton } from "./IconButton";

interface OverlayProps {
  open: boolean;
  title: string;
  description?: string;
  onClose: () => void;
  children: ReactNode;
}

function useEscape(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return;
    const handleKeyDown = (event: KeyboardEvent) => event.key === "Escape" && onClose();
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose, open]);
}

export function Drawer({ open, title, description, onClose, children }: OverlayProps) {
  useEscape(open, onClose);
  const reduceMotion = useReducedMotion();

  return (
    <AnimatePresence>
      {open && (
        <div className="ui-overlay" role="presentation">
          <motion.button
            aria-label="关闭抽屉"
            className="ui-overlay__backdrop"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
          />
          <motion.aside
            aria-modal="true"
            className="ui-drawer"
            initial={{ x: reduceMotion ? 0 : 36, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ x: reduceMotion ? 0 : 28, opacity: 0 }}
            role="dialog"
          >
            <header className="ui-overlay__header">
              <div><h2>{title}</h2>{description && <p>{description}</p>}</div>
              <IconButton label="关闭" onClick={onClose}><X size={18} /></IconButton>
            </header>
            <div className="ui-overlay__content">{children}</div>
          </motion.aside>
        </div>
      )}
    </AnimatePresence>
  );
}

interface ConfirmDialogProps extends Omit<OverlayProps, "children"> {
  confirmLabel?: string;
  destructive?: boolean;
  onConfirm: () => void;
}

export function ConfirmDialog({
  open,
  title,
  description,
  onClose,
  confirmLabel = "确认",
  destructive,
  onConfirm,
}: ConfirmDialogProps) {
  useEscape(open, onClose);
  const reduceMotion = useReducedMotion();

  return (
    <AnimatePresence>
      {open && (
        <div className="ui-overlay ui-overlay--centered">
          <motion.button aria-label="关闭对话框" className="ui-overlay__backdrop" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onClick={onClose} />
          <motion.div
            aria-modal="true"
            className="ui-dialog"
            initial={{ y: reduceMotion ? 0 : 10, scale: reduceMotion ? 1 : 0.98, opacity: 0 }}
            animate={{ y: 0, scale: 1, opacity: 1 }}
            exit={{ y: reduceMotion ? 0 : 6, scale: reduceMotion ? 1 : 0.99, opacity: 0 }}
            role="alertdialog"
          >
            <h2>{title}</h2>
            {description && <p>{description}</p>}
            <footer>
              <Button variant="secondary" onClick={onClose}>取消</Button>
              <Button variant={destructive ? "danger" : "primary"} onClick={onConfirm}>{confirmLabel}</Button>
            </footer>
          </motion.div>
        </div>
      )}
    </AnimatePresence>
  );
}
