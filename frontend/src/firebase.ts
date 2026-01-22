import { initializeApp } from 'firebase/app';
import { getAuth } from 'firebase/auth';
import { getFirestore } from 'firebase/firestore';

const firebaseConfig = {
  apiKey: "AIzaSyCAMOeSuQhJIzNnUZOJxT6b3o97RpTHPuA",
  authDomain: "tsundoku-4c197.firebaseapp.com",
  projectId: "tsundoku-4c197",
  storageBucket: "tsundoku-4c197.firebasestorage.app",
  messagingSenderId: "888634053269",
  appId: "1:888634053269:web:dd7ef7266b86f02bab019c",
  measurementId: "G-LRW5N0N51F"
};

// Initialize Firebase
const app = initializeApp(firebaseConfig);
const auth = getAuth(app);
const db = getFirestore(app);

export { app, auth, db };
