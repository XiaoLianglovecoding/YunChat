import { Component, type ErrorInfo, type ReactNode } from "react";
import { CircleAlert, RefreshCw } from "lucide-react";
import { Button } from "../ui";

interface ErrorBoundaryProps { children: ReactNode; }
interface ErrorBoundaryState { error: Error | null; }

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    if (import.meta.env.DEV) console.error("GoIM render failure", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return <main className="fatal-error"><span><CircleAlert size={24} /></span><h1>页面暂时无法显示</h1><p>应用遇到了一个意外问题。你的登录信息和本地输入不会被主动清除。</p><Button leadingIcon={<RefreshCw size={15} />} onClick={() => window.location.reload()}>重新加载</Button>{import.meta.env.DEV && <details><summary>开发信息</summary><code>{this.state.error.message}</code></details>}</main>;
  }
}
