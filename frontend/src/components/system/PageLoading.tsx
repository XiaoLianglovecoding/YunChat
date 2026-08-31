import { LoaderCircle } from "lucide-react";

export function PageLoading() {
  return <main className="page-loading" aria-label="页面加载中"><LoaderCircle className="ui-spinner" size={21} /><span>正在加载界面…</span></main>;
}
