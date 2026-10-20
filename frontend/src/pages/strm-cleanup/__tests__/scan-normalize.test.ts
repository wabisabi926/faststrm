import { describe, expect, it } from "vitest";
import { normalizeScanResponse } from "../scan-normalize";

describe("normalizeScanResponse", () => {
  it("保留 scanId / scanExpiresAt / unmanaged 计数", () => {
    const result = normalizeScanResponse(
      {
        scanId: "scan-123",
        scanExpiresAt: 123456,
        totalUnmanaged: 2,
        mappings: [
          {
            account: "alice",
            cloudPath: "/x",
            localPath: "/media",
            remoteFileCount: 1,
            localStrmCount: 2,
            unmanagedCount: 2,
            staleStrms: [{ relPath: "a.strm" }],
            missingStrms: [],
          },
        ],
      },
      "mapping"
    );
    expect(result.scanId).toBe("scan-123");
    expect(result.scanExpiresAt).toBe(123456);
    expect(result.totalUnmanaged).toBe(2);
    expect(result.mappings[0].unmanagedCount).toBe(2);
    expect(result.mappings[0].staleStrms[0].localPath).toBe("/media");
    expect(result.mappings[0].staleStrms[0].mappingId).toBe("mapping-0");
  });
});
