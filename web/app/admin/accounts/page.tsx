"use client";

import { useEffect } from "react";

export default function AccountsPage() {
  useEffect(() => {
    window.location.replace("/admin/channels");
  }, []);
  return null;
}
