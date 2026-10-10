/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";

import type { StoreCatalogueProduct } from "./catalogue-types";
import {
  storeProductDisplayTags,
  type StoreStockTagProduct,
} from "./stock-status";

const STORE_CATALOGUE_TAG_LABELS = {
  in_stock: "In stock",
  out_of_stock: "Out of stock",
  trading_paused: "Trading paused",
  auto_delivery: "Automatic delivery",
  ai_processing: "AI processing",
  guest_purchase: "Guest purchase available",
} as const;

export function StoreCatalogueTags({
  product,
}: {
  product: StoreStockTagProduct & Pick<StoreCatalogueProduct, "catalogue">;
}) {
  const { t } = useTranslation();
  // Normalize older visibility-aware projections without inferring checkout
  // permission from physical inventory or collection credentials.
  const tags = storeProductDisplayTags(product);
  const customTags = [...new Set(product.catalogue?.custom_tags ?? [])];
  if (!tags.length && !customTags.length) return null;
  return (
    <div className="flex flex-wrap gap-1" aria-label={t("Product tags")}>
      {tags.map((tag) => (
        <Badge
          key={tag}
          variant="secondary"
          className="max-w-full break-words whitespace-normal"
          title={
            tag === "trading_paused"
              ? t("This product or payment method is currently unavailable.")
              : undefined
          }
        >
          {tag in STORE_CATALOGUE_TAG_LABELS
            ? t(
                STORE_CATALOGUE_TAG_LABELS[
                  tag as keyof typeof STORE_CATALOGUE_TAG_LABELS
                ],
              )
            : tag}
        </Badge>
      ))}
      {customTags.map((tag) => (
        <Badge
          key={`custom-${tag}`}
          variant="outline"
          className="max-w-full break-words whitespace-normal"
        >
          {tag}
        </Badge>
      ))}
    </div>
  );
}
