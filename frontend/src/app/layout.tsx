import type { Metadata } from "next";
import  LayoutWrapper  from "@/components/LayoutWrapper";
import ClientAuthProvider from "@/components/ClientAuthProvider";
import { Toaster } from "@/components/ui/sonner"
import "./globals.css";

export const metadata: Metadata = {
  title: "Fast Strm",
  description: "更快、更强、更硬",
  icons: {
    icon: "/favicon.ico",
    shortcut: "/favicon.ico",
    apple: "/logo.png",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className="antialiased">
        <ClientAuthProvider>
          <LayoutWrapper>{children}</LayoutWrapper>
        </ClientAuthProvider>
        <Toaster />
      </body>
    </html>
  );
}
