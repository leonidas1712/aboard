import type { Metadata, Viewport } from "next";
import type { ReactNode } from "react";
import "./globals.css";
import { firstPaint } from "./themes";

// The tab icons (icon.svg, icon.png, apple-icon.png in this folder, and favicon.ico in
// public/ for browsers that ask for it) are the brand's mark as it looks in dark, on a
// near-black tile, whatever the theme; the larger ones in public/ are for a home screen.
export const metadata: Metadata = { title: "aboard", manifest: "/manifest.webmanifest" };

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  // The page draws to the screen's edges and keeps its controls inside the safe areas;
  // the on-screen keyboard shrinks the page, so the message box stays above it.
  viewportFit: "cover",
  interactiveWidget: "resizes-content",
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#fafaf7" },
    { media: "(prefers-color-scheme: dark)", color: "#0c0d0a" },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        {/* The colour scheme this browser chose, applied before the first paint. */}
        <script dangerouslySetInnerHTML={{ __html: firstPaint }} />
        <link rel="preload" href="/fonts/geist-variable.woff2" as="font" type="font/woff2" crossOrigin="" />
      </head>
      <body>{children}</body>
    </html>
  );
}
