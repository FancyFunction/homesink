# Homesink

Homesink is a media file synchronization solution to transfer photos, videos and audio files from personal devices to a central data storage (sink) located in a trusted local network (home).

The solution is thus compromised of a mobile app for the user and a server application for a static device in the trusted network.

## Mobile App

### Functionality

Check how many pictures, videos or audio recordings have been created since the last synchronization.
Let the user configure a threshold in the settings when they want to be notified and take 10 as the default value.
If the threshold of unsynchronized files has been reached notify the user.
If there are less than unsynchronized files than the configured threshold, wait until the end of the week to notify the user.
Do not notify the user if no files have been created that week.

If the user clicks the notification for synchronization, a screen with the list of to be synchronized files should appear.
The user should see a small thumbnail for each file.
If the thumbnail is clicked, the original file will be displayed.

Each file has two options, "upload" and "upload and remove from device".
Videos larger than 30 MB should automatically be selected with "upload and remove from device", Images should always be selected only as "upload".
All files should be pre selected for synchronization, the user needs to manually deselect the files that should not be synchronized.
Only delete a local file if the backend server returned a successful status for that file.

At the bottom of the screen is a button "Synchronize now" that starts the synchronization.
During the synchronization the user should see a notification that displays the current status, an overall percentage with a progress bar and a file counter, for example "29% done - 14/47 Files synchronized".
If the user clicks the progress notification, a screen opens displaying the current synchronization queue.

Include the app version when communicating with the backend server.
If the backend server contains a newer version, prompt the end user to update the mobile app.
Do not use a notification for an update but include a banner at the top inside the app.

### User Experience

Do not annoy the user with notifications.
The user should be notified at most once a day.

Start notifying the user at 22:00 and keep track of when the user interacts with the notifications or opens the app directly.
Based on the tracked data, find the best time to ask the user for an interaction (if applicable).
Analyze each day of the week separately, the user might have a different preferred time on a weekday compared to the weekend.

Let the user override this behavior in the settings with a toggle option "Custom synchronization schedule" and an explanation for the toggle "Configure when you want to be asked to synchronize your files".
If "Custom synchronization schedule" is enabled, another toggle appears "Same time for every day".
If "Same time for every day" is enabled, simply display an option to select the time.
If "Same time for every day" is disabled, list all days of the week and show a time selection option for each one.

### User Interface

Localize all strings in the app in german language.

The user interface of the app should consist of a navigation bar at the bottom that changes the rest of the display area.
Main screens are:
- Synchronization list
- Backend file browser
- Synchronization queue (also displays finished file uploads, most recent files at the top)
- Settings

Use the following color scheme:
```CSS
/* Primary: Charybdis */
--primary-100: #F2F9FF;
--primary-200: #BCE5FE;
--primary-300: #84D3F8;
--primary-400: #4AC0E8;
--primary-500: #16A6C9;
--primary-600: #078BA1;
--primary-700: #026E78;
--primary-800: #004D4F;
--primary-900: #002625;

/* Accent: End of Summer */
--accent-100: #FFFEF2;
--accent-200: #FEF6BA;
--accent-300: #F8E280;
--accent-400: #E7C045;
--accent-500: #C79010;
--accent-600: #9F6805;
--accent-700: #774601;
--accent-800: #4E2900;
--accent-900: #261200;

/* Neutral */
--neutral-100: #FAFBFC;
--neutral-200: #E5E9EB;
--neutral-300: #D1D7DA;
--neutral-400: #BEC6C9;
--neutral-500: #ABB5B8;
--neutral-600: #889193;
--neutral-700: #656E6F;
--neutral-800: #444A4B;
--neutral-900: #222626;
```

### Deployment & Distribution

The mobile app should be packaged as an .apk file that can be installed by the user directly.
The package will be distributed by backend server.

## Backend Server

### Functionality

The most important function for the backend server is to accept various media files and store them in a connected storage device (external data drive, NAS, etc.).

Check for duplicate files by requiring a hash of the files that will be uploaded before the file is actually uploaded.
After the file has been uploaded, validate the given hash value before the file is ultimately stored in the final location.
If the provided file hash and the actual hash after upload do not match, return an error to the client and remove the file from the backend system.

Order files in a hierarchy of album name, year and month so that they can be browsed easily in case the storage device is accessed through other means.

Provide updates to the mobile client application.
Check the mobile client version and notify the client if a new version is available on the backend server.

Offer the ability to browse the files through the mobile app.
Generate small thumbnails for a quick preview of images.

Videos files should be compressed to reduce the necessary file storage without losing too much information.
Try to find the best balance to still display videos nicely at 2k resolution.
Do video compressions asynchronously so that the client is not waiting for compressions to be finished.

Optimize the communication with the client to be as fast and efficient as possible.

### Deployment & Distribution

The backend server should ultimately be packaged as a docker image and be set up so that it can run from the image with minimal configuration.
Make it so the backend server automatically checks a remote location (for example Github) for a new version and automatically updates itself.
Analyze different approaches for the server setup on a Linux Mint machine or a Ubuntu server and ask for further clarification where needed.