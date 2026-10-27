import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Fritz",
  description: "Fritz — Meta Global AI Hackathon Personal Assistant",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
