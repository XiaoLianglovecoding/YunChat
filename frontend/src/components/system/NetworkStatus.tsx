import { AnimatePresence, motion, useReducedMotion } from "framer-motion";
import { WifiOff } from "lucide-react";
import { useEffect, useState } from "react";

export function NetworkStatus() {
  const [online, setOnline] = useState(() => navigator.onLine);
  const reduceMotion = useReducedMotion();

  useEffect(() => {
    const handleOnline = () => setOnline(true);
    const handleOffline = () => setOnline(false);
    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);
    return () => { window.removeEventListener("online", handleOnline); window.removeEventListener("offline", handleOffline); };
  }, []);

  return <AnimatePresence>{!online && <motion.div animate={{ opacity: 1, y: 0 }} className="network-status" exit={{ opacity: 0, y: reduceMotion ? 0 : -6 }} initial={{ opacity: 0, y: reduceMotion ? 0 : -6 }} role="status"><WifiOff size={14} />网络已断开，待发送内容会保留</motion.div>}</AnimatePresence>;
}
