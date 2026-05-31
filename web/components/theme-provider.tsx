"use client";

import { useEffect } from "react";
import { publicApi } from "@/lib/api";
import type { PublicSettings } from "@/types/api";

const STORAGE_KEY = "uapi.ui.settings";

function applyTheme(settings: PublicSettings) {
  document.body.dataset.background = "mesh";
  if (settings.wallpaper_url) {
    document.body.style.setProperty("--wallpaper-image", `url("${settings.wallpaper_url}")`);
  } else {
    document.body.style.removeProperty("--wallpaper-image");
  }
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  useEffect(() => {
    try {
      const cached = window.localStorage.getItem(STORAGE_KEY);
      if (cached) applyTheme(JSON.parse(cached) as PublicSettings);
    } catch {
      window.localStorage.removeItem(STORAGE_KEY);
    }

    publicApi.settings().then((settings) => {
      applyTheme(settings);
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(settings));
    }).catch(() => undefined);
  }, []);

  return (
    <>
      <div className="wallpaper-canvas" aria-hidden="true" />
      {children}
    </>
  );
}
