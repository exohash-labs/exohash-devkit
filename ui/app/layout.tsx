import type { Metadata } from "next";
import { JetBrains_Mono, Space_Grotesk, Orbitron } from "next/font/google";
import "./globals.css";
import { Providers } from "@/components/Providers";

const mono = JetBrains_Mono({
  subsets: ["latin"],
  variable: "--font-mono",
  weight: ["400", "500", "600", "700"],
});

const display = Space_Grotesk({
  subsets: ["latin"],
  variable: "--font-display",
  weight: ["400", "500", "600", "700"],
});

const orbitron = Orbitron({
  subsets: ["latin"],
  variable: "--font-orbitron",
  weight: ["400", "500", "600", "700", "800", "900"],
});

export const metadata: Metadata = {
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL || "https://play.devnet.exohash.io"),
  title: {
    default: "ExoHash Play — Devnet",
    template: "%s | ExoHash Play",
  },
  description:
    "Play crash, dice, and mines on a real blockchain. No deposits required.",
  icons: {
    icon: "/favicon.ico",
    apple: "/apple-icon.png",
  },
  openGraph: {
    title: "ExoHash Play — Devnet",
    description:
      "Play crash, dice, and mines on a real blockchain. No deposits required.",
    siteName: "ExoHash Play",
    type: "website",
    images: [{ url: "/og.jpg", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: "ExoHash Play — Devnet",
    description:
      "Play crash, dice, and mines on a real blockchain. No deposits required.",
    creator: "@ExoHashIO",
    images: ["/og.jpg"],
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <head>
        <script
          dangerouslySetInnerHTML={{
            __html:
              "if(location.pathname==='/'&&!localStorage.getItem('exohash_intro_seen'))location.replace('/intro');",
          }}
        />
      </head>
      <body className={`${mono.variable} ${display.variable} ${orbitron.variable} font-mono antialiased`}>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
