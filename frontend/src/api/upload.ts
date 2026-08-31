import type { UploadAvatarResponse } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createUploadApi = (client: GoIMApiClient) => ({
  avatar: (file: File) => {
    const form = new FormData();
    form.set("file", file);
    return client.upload<UploadAvatarResponse>("/upload/avatar", form);
  },
});
