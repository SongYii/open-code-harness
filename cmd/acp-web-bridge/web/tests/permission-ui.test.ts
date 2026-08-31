import { describe, expect, it } from "vitest";
import { TrajectoryView } from "../src/ui";

describe("TrajectoryView composer-position permission approval", () => {
  it("replaces the composer with the request's detail and disables submission while pending", () => {
    const root = document.createElement("div");
    const view = new TrajectoryView(root);
    expect(view.isComposerDisabled()).toBe(false);

    const decision = view.showPermissionRequest({ toolTitle: "write_file", toolKind: "edit" });

    expect(view.isComposerDisabled()).toBe(true);
    const composerText = view.getComposerElement().textContent ?? "";
    expect(composerText).toContain("write_file");
    expect(composerText).toContain("edit");
    expect(view.getComposerElement().querySelector(".permission-allow")).not.toBeNull();
    expect(view.getComposerElement().querySelector(".permission-reject")).not.toBeNull();
    expect(view.getComposerElement().querySelector("input")).toBeNull();

    void decision; // resolved in a later test
  });

  it("choosing allow-once resolves with allow-once and restores the composer", async () => {
    const root = document.createElement("div");
    const view = new TrajectoryView(root);

    const decision = view.showPermissionRequest({ toolTitle: "exec", toolKind: "execute" });
    const allowButton = view.getComposerElement().querySelector<HTMLButtonElement>(".permission-allow");
    expect(allowButton).not.toBeNull();
    allowButton?.click();

    await expect(decision).resolves.toBe("allow-once");
    expect(view.isComposerDisabled()).toBe(false);
    expect(view.getComposerElement().querySelector("input")).not.toBeNull();
    expect(view.getComposerElement().querySelector(".permission-allow")).toBeNull();
  });

  it("choosing reject resolves with reject-once and restores the composer", async () => {
    const root = document.createElement("div");
    const view = new TrajectoryView(root);

    const decision = view.showPermissionRequest({ toolTitle: "exec", toolKind: "execute" });
    const rejectButton = view.getComposerElement().querySelector<HTMLButtonElement>(".permission-reject");
    rejectButton?.click();

    await expect(decision).resolves.toBe("reject-once");
    expect(view.isComposerDisabled()).toBe(false);
  });

  it("queues a second permission request arriving while the first is pending, rather than corrupting state", async () => {
    const root = document.createElement("div");
    const view = new TrajectoryView(root);

    const first = view.showPermissionRequest({ toolTitle: "read_file", toolKind: "read" });
    const second = view.showPermissionRequest({ toolTitle: "write_file", toolKind: "edit" });

    // The second request must not have replaced the first's rendering.
    expect(view.getComposerElement().textContent ?? "").toContain("read_file");
    expect(view.getComposerElement().textContent ?? "").not.toContain("write_file");

    view.getComposerElement().querySelector<HTMLButtonElement>(".permission-allow")?.click();
    await expect(first).resolves.toBe("allow-once");

    // The queued second request must now be rendered and still pending.
    expect(view.isComposerDisabled()).toBe(true);
    expect(view.getComposerElement().textContent ?? "").toContain("write_file");

    view.getComposerElement().querySelector<HTMLButtonElement>(".permission-reject")?.click();
    await expect(second).resolves.toBe("reject-once");
    expect(view.isComposerDisabled()).toBe(false);
  });
});
