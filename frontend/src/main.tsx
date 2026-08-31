import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import RootApp from "./RootApp";
import "./styles.css";
import "./app.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RootApp />
  </StrictMode>,
);
