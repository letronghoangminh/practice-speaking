import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Practice Speaking",
  description: "English technical interview practice for DevOps and SRE engineers",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
