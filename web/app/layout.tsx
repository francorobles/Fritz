import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Fritz",
  description: "What Fritz, your desk companion, is doing and remembers",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
