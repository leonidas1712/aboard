import type { Metadata, Viewport } from "next";
import type { ReactNode } from "react";
import "./globals.css";

export const metadata: Metadata = { title: "Aboard" };

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#fbfcfc" },
    { media: "(prefers-color-scheme: dark)", color: "#14212a" },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <head>
        <link rel="preload" href="/fonts/atkinson-hyperlegible-next-latin-wght-normal.woff2" as="font" type="font/woff2" crossOrigin="" />
      </head>
      <body>{children}</body>
    </html>
  );
}
