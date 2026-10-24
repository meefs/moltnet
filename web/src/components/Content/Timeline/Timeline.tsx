import { useVirtualizer } from "@tanstack/react-virtual";
import type { KeyboardEvent, TouchEvent, WheelEvent } from "react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { useMessages } from "../../../hooks/useMessages";
import { useNetwork } from "../../../hooks/useNetwork";
import { supportsDirectMessages } from "../../../lib/capabilities";
import {
  isMessageTargetSelection,
  type Message,
} from "../../../lib/types";
import { useSelection } from "../../../providers";
import {
  captureScrollAnchor,
  captureScrollSnapshot,
  isNearTop,
  isTimelineAtBottom,
  pinToBottom,
  restoreScrollAnchor,
  prependRestoreTop,
  type ScrollAnchor,
  type ScrollSnapshot,
} from "./scroll";
import { continuesFrom, rowKeyOf, startsDay, TimelineMessage } from "./TimelineMessage";
import { DayDivider, EmptyPane } from "./TimelinePlaceholders";
import { TimelineHeader } from "./TimelineHeader";

const ESTIMATED_ROW_HEIGHT = 64;

export function Timeline() {
  const { selected } = useSelection();
  const { data: network } = useNetwork();
  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading } =
    useMessages();
  const parentRef = useRef<HTMLDivElement>(null);
  const directMessagesEnabled = supportsDirectMessages(network);
  const selectedKey = isMessageTargetSelection(selected)
    ? `${selected.kind}:${selected.id}`
    : null;

  const messages = useMemo<Message[]>(() => {
    if (!data) return [];
    // React Query stores the newest page first and appends older pages after it.
    // Each API page is already chronological, so only reverse the page groups.
    return data.pages
      .slice()
      .reverse()
      .flatMap((page) => page.messages ?? []);
  }, [data]);

  const virtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => parentRef.current,
    getItemKey: (index) => (messages[index] ? rowKeyOf(messages[index]) : index),
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: 12,
  });

  const firstMessageID = messages[0]?.id ?? "";
  const lastMessageID = messages[messages.length - 1]?.id ?? "";
  const autoFollowRef = useRef(true);
  const nearBottomRef = useRef(true);
  const bottomPinTokenRef = useRef(0);
  const pinningRef = useRef(false);
  const anchorRestoreTokenRef = useRef(0);
  const restoringAnchorRef = useRef(false);
  const scrolledKeyRef = useRef<string | null>(null);
  const scrollAnchorRef = useRef<ScrollAnchor | null>(null);
  const snapshotRef = useRef<ScrollSnapshot | null>(null);
  const touchYRef = useRef<number | null>(null);
  const renderedRef = useRef<{
    key: string | null;
    count: number;
    lastMessageID: string;
  }>({ key: null, count: 0, lastMessageID: "" });

  const cancelBottomPin = useCallback(() => {
    bottomPinTokenRef.current += 1;
    anchorRestoreTokenRef.current += 1;
    autoFollowRef.current = false;
    nearBottomRef.current = false;
    pinningRef.current = false;
    const node = parentRef.current;
    if (node) scrollAnchorRef.current = captureScrollAnchor(node);
  }, []);

  const preserveScrollAnchor = useCallback((anchor: ScrollAnchor) => {
    const token = anchorRestoreTokenRef.current + 1;
    anchorRestoreTokenRef.current = token;

    const restore = () => {
      if (anchorRestoreTokenRef.current !== token || autoFollowRef.current) return;
      const node = parentRef.current;
      if (!node) return;

      restoringAnchorRef.current = true;
      restoreScrollAnchor(node, anchor);
      scrollAnchorRef.current = captureScrollAnchor(node) ?? anchor;
      requestAnimationFrame(() => {
        if (anchorRestoreTokenRef.current === token) {
          restoringAnchorRef.current = false;
        }
      });
    };

    restore();
    requestAnimationFrame(restore);
  }, []);

  const scrollToBottom = useCallback(() => {
    const token = bottomPinTokenRef.current + 1;
    bottomPinTokenRef.current = token;
    autoFollowRef.current = true;
    const stick = () => {
      if (bottomPinTokenRef.current !== token || !autoFollowRef.current) return;
      const node = parentRef.current;
      if (!node) return;
      pinningRef.current = true;
      pinToBottom(node);
      nearBottomRef.current = true;
      requestAnimationFrame(() => {
        if (bottomPinTokenRef.current === token) {
          pinningRef.current = false;
        }
      });
    };

    stick();
    requestAnimationFrame(() => {
      stick();
      requestAnimationFrame(stick);
    });
    window.setTimeout(stick, 50);
  }, []);

  const maybeFetchOlder = useCallback(
    (node: HTMLDivElement | null) => {
      if (
        !node ||
        messages.length === 0 ||
        !hasNextPage ||
        isFetchingNextPage ||
        !isNearTop(node)
      ) {
        return;
      }

      // Bookmark the row at the top of the viewport from the virtualizer's
      // measurements: right after a fast scroll the DOM may not hold the
      // rows for this offset yet.
      const item = virtualizer.getVirtualItemForOffset(node.scrollTop);
      const message = item ? messages[item.index] : undefined;
      snapshotRef.current = {
        ...captureScrollSnapshot(node),
        prepend: item && message ? { rowKey: rowKeyOf(message), endOffset: item.end - node.scrollTop } : null,
      };
      void fetchNextPage();
    },
    [fetchNextPage, hasNextPage, isFetchingNextPage, messages, virtualizer],
  );

  const handleScroll = useCallback(() => {
    const node = parentRef.current;
    if (!node) return;
    const nearBottom = isTimelineAtBottom(node);
    nearBottomRef.current = nearBottom;
    if (nearBottom) {
      autoFollowRef.current = true;
      scrollAnchorRef.current = null;
    } else if (restoringAnchorRef.current) {
      scrollAnchorRef.current = captureScrollAnchor(node);
    } else if (!pinningRef.current) {
      autoFollowRef.current = false;
      bottomPinTokenRef.current += 1;
      anchorRestoreTokenRef.current += 1;
      scrollAnchorRef.current = captureScrollAnchor(node);
    }
    maybeFetchOlder(node);
  }, [maybeFetchOlder]);

  const handleWheel = useCallback(
    (event: WheelEvent<HTMLDivElement>) => {
      if (event.deltaY < 0) cancelBottomPin();
    },
    [cancelBottomPin],
  );

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.key === "ArrowUp" || event.key === "PageUp" || event.key === "Home") {
        cancelBottomPin();
      }
    },
    [cancelBottomPin],
  );

  const handleTouchStart = useCallback((event: TouchEvent<HTMLDivElement>) => {
    touchYRef.current = event.touches[0]?.clientY ?? null;
  }, []);

  const handleTouchMove = useCallback(
    (event: TouchEvent<HTMLDivElement>) => {
      const currentY = event.touches[0]?.clientY ?? null;
      const previousY = touchYRef.current;
      if (currentY !== null && previousY !== null && currentY > previousY + 4) {
        cancelBottomPin();
      }
      touchYRef.current = currentY;
    },
    [cancelBottomPin],
  );

  // Preserve the current viewport when older pages are prepended above it.
  useLayoutEffect(() => {
    const node = parentRef.current;
    const snapshot = snapshotRef.current;
    if (isFetchingNextPage || !node || !snapshot) return;
    anchorRestoreTokenRef.current += 1;

    const anchorIndex = snapshot.prepend
      ? messages.findIndex((message) => rowKeyOf(message) === snapshot.prepend?.rowKey)
      : -1;
    const restore = () => {
      const current = parentRef.current;
      if (!current) return;
      // getTotalSize() rebuilds measurements from the latest row sizes first.
      if (anchorIndex >= 0) virtualizer.getTotalSize();
      const anchorEnd = anchorIndex >= 0 ? virtualizer.measurementsCache[anchorIndex]?.end : undefined;
      current.scrollTop = prependRestoreTop(current, snapshot, anchorEnd);
      const nearBottom = isTimelineAtBottom(current);
      nearBottomRef.current = nearBottom;
      if (nearBottom) autoFollowRef.current = true;
      scrollAnchorRef.current = captureScrollAnchor(current);
    };

    restore();
    requestAnimationFrame(() => {
      restore();
      requestAnimationFrame(restore);
    });
    snapshotRef.current = null;
  }, [firstMessageID, isFetchingNextPage, lastMessageID, messages, virtualizer]);

  // Initial selection loads pin to the newest message. Later live appends only
  // stick when the operator was already reading at the bottom of the chat.
  useLayoutEffect(() => {
    const previous = renderedRef.current;
    const targetChanged = previous.key !== selectedKey;
    const tailChanged =
      !targetChanged &&
      lastMessageID !== "" &&
      lastMessageID !== previous.lastMessageID;
    const countGrew = !targetChanged && messages.length > previous.count;

    renderedRef.current = {
      key: selectedKey,
      count: messages.length,
      lastMessageID,
    };

    if (!selectedKey) {
      scrolledKeyRef.current = null;
      nearBottomRef.current = true;
      autoFollowRef.current = true;
      scrollAnchorRef.current = null;
      snapshotRef.current = null;
      return;
    }
    if (targetChanged) {
      scrolledKeyRef.current = null;
      nearBottomRef.current = true;
      autoFollowRef.current = true;
      scrollAnchorRef.current = null;
      snapshotRef.current = null;
    }
    if (messages.length === 0) return;
    if (scrolledKeyRef.current !== selectedKey) {
      scrolledKeyRef.current = selectedKey;
      scrollToBottom();
      return;
    }
    if (snapshotRef.current || isFetchingNextPage) return;
    const node = parentRef.current;
    const shouldStick =
      autoFollowRef.current && (nearBottomRef.current || (!!node && isTimelineAtBottom(node)));
    if ((tailChanged || countGrew) && shouldStick) {
      scrollToBottom();
      return;
    }
    if ((tailChanged || countGrew) && node && !autoFollowRef.current) {
      const anchor = scrollAnchorRef.current ?? captureScrollAnchor(node);
      if (anchor) preserveScrollAnchor(anchor);
    }
  }, [
    isFetchingNextPage,
    lastMessageID,
    messages.length,
    preserveScrollAnchor,
    scrollToBottom,
    selectedKey,
  ]);

  // Rows can grow after they were measured (an image or audio player loading).
  // The virtualizer only compensates for rows above the viewport, so while the
  // operator is following the newest message, keep the bottom pinned.
  const contentRef = useRef<HTMLDivElement>(null);
  const hasMessages = messages.length > 0;
  useEffect(() => {
    const node = contentRef.current;
    if (!node || typeof ResizeObserver === "undefined") return;
    // scrollToBottom marks the scroll as programmatic, so handleScroll does not
    // mistake it for the operator scrolling away and turn following off.
    const observer = new ResizeObserver(() => {
      if (!autoFollowRef.current || snapshotRef.current) return;
      scrollToBottom();
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, [hasMessages, selectedKey, scrollToBottom]);

  // ─── Trigger fetch-older when the user scrolls near the top ─────────────────
  useEffect(() => {
    maybeFetchOlder(parentRef.current);
  }, [firstMessageID, maybeFetchOlder, messages.length]);

  if (!isMessageTargetSelection(selected)) {
    const targetLabel = directMessagesEnabled ? "a room or direct channel" : "a room";
    return <EmptyPane text={`select ${targetLabel}.`} />;
  }

  if (selected.kind === "dm" && !directMessagesEnabled) {
    return <EmptyPane text="direct channels are disabled for this network." />;
  }

  return (
    <section className="flex flex-col min-h-0 min-w-0">
      <TimelineHeader selection={selected} loaded={messages.length} hasOlder={!!hasNextPage} />
        <div
          ref={parentRef}
          className="flex-1 overflow-auto"
          data-testid="timeline-scroll"
          style={{ overflowAnchor: "none" }}
          onKeyDown={handleKeyDown}
          onScroll={handleScroll}
          onTouchMove={handleTouchMove}
          onTouchStart={handleTouchStart}
          onWheel={handleWheel}
        >
          {messages.length === 0 ? (
            <p className="text-faint text-[14px] px-5 py-6">
              {isLoading ? "loading…" : "no messages yet."}
            </p>
          ) : (
            <div
              ref={contentRef}
              style={{
                height: `${virtualizer.getTotalSize()}px`,
                width: "100%",
                position: "relative",
              }}
            >
              {hasNextPage && isFetchingNextPage ? (
                <div className="absolute top-0 left-0 right-0 text-center font-mono text-[11px] text-faint py-2 pointer-events-none">
                  loading older…
                </div>
              ) : null}
              {virtualizer.getVirtualItems().map((virtualItem) => {
                const message = messages[virtualItem.index];
                if (!message) return null;
                return (
                  <div
                    key={virtualItem.key}
                    data-index={virtualItem.index}
                    data-message-id={message.id}
                    ref={virtualizer.measureElement}
                    style={{
                      position: "absolute",
                      top: 0,
                      left: 0,
                      width: "100%",
                      transform: `translateY(${virtualItem.start}px)`,
                      paddingLeft: 16,
                      paddingRight: 20,
                    }}
                  >
                    {startsDay(messages[virtualItem.index - 1], message) ? (
                      <DayDivider date={message.created_at} />
                    ) : null}
                    <TimelineMessage
                      message={message}
                      continued={continuesFrom(messages[virtualItem.index - 1], message)}
                    />
                  </div>
                );
              })}
            </div>
          )}
          <div className="h-3" aria-hidden="true" />
        </div>
    </section>
  );
}
