"use client";

import { useMemo, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import {
  CodeBlock,
  DiagramZoomDialog,
  PreviewFrame,
  PreviewToggleButton,
  SvgDownloadButton,
  downloadSvgFile,
} from "./MermaidBlock";

const SVG_NS = "http://www.w3.org/2000/svg";

function parsedRoot(source: string, parser: DOMParser): Element | null {
  const doc = parser.parseFromString(source, "image/svg+xml");
  // A syntax error comes back as a <parsererror> element: the root in Firefox,
  // inside the partial document in Chrome and Safari.
  if (doc.getElementsByTagName("parsererror").length > 0) return null;
  return doc.documentElement;
}

/**
 * The fence's text as an SVG file, or null when it is not one: well-formed XML
 * with an <svg> root in the SVG namespace. An <svg> written into HTML needs no
 * xmlns, an SVG image does, so a root without one gets it.
 */
export function svgImageSource(code: string, parser: DOMParser): string | null {
  let source = code;
  let root = parsedRoot(source, parser);
  if (root?.localName === "svg" && root.namespaceURI === null && !root.hasAttribute("xmlns")) {
    source = code.replace(/<svg(?=[\s/>])/, `<svg xmlns="${SVG_NS}"`);
    root = parsedRoot(source, parser);
  }
  return root?.localName === "svg" && root.namespaceURI === SVG_NS ? source : null;
}

export function svgDataUrl(source: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(source)}`;
}

/**
 * A ```svg fence, shown like a mermaid one. The picture is only ever an <img>:
 * the browser runs no script or event handler of an SVG image and loads
 * nothing it links to, so the fence's text never enters the page's DOM.
 */
export function SvgBlock({ code, isStreaming }: { code: string; isStreaming?: boolean }) {
  const { t } = useI18n();
  const [showPreview, setShowPreview] = useState(true);
  const [zoomOpen, setZoomOpen] = useState(false);
  const [failedUrl, setFailedUrl] = useState<string | null>(null);
  const [unsizedUrl, setUnsizedUrl] = useState<string | null>(null);
  const previewVisible = showPreview && !isStreaming;
  // undefined: not parsed (a server render has no DOMParser).
  const source = useMemo(
    () => (previewVisible && typeof DOMParser !== "undefined" ? svgImageSource(code, new DOMParser()) : undefined),
    [code, previewVisible],
  );
  const url = useMemo(() => (source ? svgDataUrl(source) : null), [source]);

  const previewButton = useMemo(() => (
    <PreviewToggleButton
      isStreaming={isStreaming}
      previewVisible={previewVisible}
      onToggle={() => setShowPreview((v) => !v)}
      title={previewVisible ? t("i18n.showSvgSource") : t("i18n.previewSvg")}
    />
  ), [isStreaming, previewVisible, t]);

  if (!previewVisible) {
    return <CodeBlock code={code} lang="svg" headerAction={previewButton} isStreaming={isStreaming} />;
  }

  const ready = url !== null && failedUrl !== url;
  const image = (className: string) => (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={url ?? undefined}
      alt={t("i18n.svgImage")}
      // Neither a viewBox nor a width and height: the image has no size of its own.
      className={[className, unsizedUrl === url ? "is-unsized" : ""].filter(Boolean).join(" ")}
      onLoad={(event) => {
        const { naturalWidth, naturalHeight } = event.currentTarget;
        if (!naturalWidth || !naturalHeight) setUnsizedUrl(url);
      }}
      onError={() => setFailedUrl(url)}
    />
  );

  return (
    <PreviewFrame
      lang="svg"
      actions={
        <>
          {ready && source && <SvgDownloadButton onClick={() => downloadSvgFile(source, "image.svg")} />}
          {previewButton}
        </>
      }
    >
      {source === undefined ? (
        <div className="mermaid-block mermaid-block-loading" />
      ) : !ready ? (
        <div className="mermaid-block mermaid-block-error">{t("i18n.invalidSvg")}</div>
      ) : (
        <>
          {!zoomOpen && (
            <button
              type="button"
              className="mermaid-block mermaid-preview-button"
              title={t("i18n.openSvgViewer")}
              aria-label={t("i18n.openSvgViewer")}
              onClick={() => setZoomOpen(true)}
            >
              {image("svg-block-image")}
            </button>
          )}
          {zoomOpen && (
            <DiagramZoomDialog title={t("i18n.svgImage")} label={t("i18n.svgViewer")} onClose={() => setZoomOpen(false)}>
              {image("svg-zoom-image")}
            </DiagramZoomDialog>
          )}
        </>
      )}
    </PreviewFrame>
  );
}
